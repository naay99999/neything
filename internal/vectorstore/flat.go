package vectorstore

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// On-disk format (current, "new" format):
//
//	[4 bytes] magic   = flatMagic (uint32 LE) -- distinguishes from the old
//	                    headerless format below; no real ID is ever long
//	                    enough to collide with this value when misread as
//	                    an idLen.
//	[1 byte]  version = flatVersion
//	then a stream of records, each:
//	  [1 byte]  type: flatRecordData or flatRecordTombstone
//	  [4 bytes] idLen (uint32 LE)
//	  [idLen]   id bytes
//	  data records only:
//	    [4 bytes] dimCount (uint32 LE)
//	    [dimCount*4 bytes] float32 vector, LE
//
// A data record for an ID overrides any earlier record for that ID (either
// a previous data record, e.g. an updated vector, or logically "undoes" a
// still-pending delete). A tombstone record removes the ID. Replaying the
// stream in order and applying last-write-wins yields the live item set.
//
// This lets Flush append new/changed records to the tail of the file
// (cheap: no read of existing data, fsync-append) instead of rewriting the
// whole file on every flush. A full compacting rewrite (tmp+rename) is only
// needed once garbage (superseded records + tombstones) crosses a
// threshold, or once, lazily, to migrate a pre-existing old-format file.
//
// Old format (still readable, never written): no header at all -- the file
// is directly a stream of data records shaped like the "data record" above
// but without the leading type byte. loadFlatVectors's caller distinguishes
// the two by peeking the first 4 bytes: old-format files start with a
// (small) idLen, which can never equal flatMagic.
const (
	flatMagic   uint32 = 0xFEEDC0DE
	flatVersion byte   = 1

	flatRecordData      byte = 0
	flatRecordTombstone byte = 1
)

// Compaction thresholds: a full rewrite is skipped in favor of a cheap
// append as long as garbage (tombstones + superseded records) stays below
// this fraction of the total on-disk record count. Small files always
// compact away any garbage immediately since a full rewrite is cheap there
// anyway.
const (
	compactGarbageFraction = 0.3
	compactMinRecords      = 512
)

// flatFile manages incremental persistence of a flat vector file shared by
// BruteForceStore and HNSWStore. It tracks which item IDs are already
// durable on disk so Flush can append only what changed instead of
// rewriting the whole file every time, falling back to a full compacting
// rewrite when on-disk garbage (deleted/superseded records) crosses
// compactGarbageFraction, or once, lazily, when the file predates this
// format.
//
// Not safe for concurrent use; callers (BruteForceStore, HNSWStore) must
// serialize access with their own mutex, same as the rest of their state.
type flatFile struct {
	path string

	onDisk         map[string]struct{}
	pendingItems   map[string]VectorItem
	pendingDeletes map[string]struct{}

	diskRecords int // total records (data+tombstone) physically in the file
	diskGarbage int // of diskRecords, how many are dead (superseded or tombstoned)

	legacyFormat bool // loaded file predates the header; next flush must migrate it
}

func newFlatFile(path string) *flatFile {
	return &flatFile{
		path:           path,
		onDisk:         make(map[string]struct{}),
		pendingItems:   make(map[string]VectorItem),
		pendingDeletes: make(map[string]struct{}),
	}
}

// load reads the file at f.path (old or new format) and returns its items.
// A missing file is not an error: it returns (nil, nil) for a brand-new
// store. Bookkeeping (onDisk/diskRecords/diskGarbage/legacyFormat) is
// initialized to match what was read.
func (f *flatFile) load() ([]VectorItem, error) {
	file, err := os.Open(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	br := bufio.NewReaderSize(file, 1<<20)

	head := make([]byte, 4)
	n, err := io.ReadFull(br, head)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			// Empty (or near-empty/truncated-to-nothing) file: nothing to
			// migrate, nothing to load.
			return nil, nil
		}
		return nil, fmt.Errorf("read header: %w", err)
	}

	if n == 4 && binary.LittleEndian.Uint32(head) == flatMagic {
		var version byte
		if err := binary.Read(br, binary.LittleEndian, &version); err != nil {
			return nil, fmt.Errorf("read version: %w", err)
		}
		items, recordCount, err := readFlatRecords(br)
		if err != nil {
			return nil, err
		}
		f.diskRecords = recordCount
		f.diskGarbage = recordCount - len(items)
		f.onDisk = make(map[string]struct{}, len(items))
		for _, it := range items {
			f.onDisk[it.ID] = struct{}{}
		}
		f.legacyFormat = false
		return items, nil
	}

	// Old (headerless) format: the 4 bytes already read are the idLen of
	// the first record. Feed them back in front of the rest of the stream.
	combined := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(head), br), 1<<20)
	items, err := loadLegacyFlatVectors(combined)
	if err != nil {
		return nil, err
	}
	f.diskRecords = len(items)
	f.diskGarbage = 0
	f.onDisk = make(map[string]struct{}, len(items))
	for _, it := range items {
		f.onDisk[it.ID] = struct{}{}
	}
	f.legacyFormat = true
	return items, nil
}

// stageAdd records that item was added or updated and must be persisted on
// the next flush.
func (f *flatFile) stageAdd(item VectorItem) {
	if _, alreadyPending := f.pendingItems[item.ID]; !alreadyPending {
		if _, live := f.onDisk[item.ID]; live {
			// The current on-disk record for this ID is about to be
			// superseded by whatever we write next flush.
			f.diskGarbage++
		}
	}
	delete(f.pendingDeletes, item.ID)
	f.pendingItems[item.ID] = item
}

// stageDelete records that id was removed and must be persisted (as a
// tombstone, or simply dropped from the pending set) on the next flush.
func (f *flatFile) stageDelete(id string) {
	delete(f.pendingItems, id)
	if _, live := f.onDisk[id]; live {
		delete(f.onDisk, id)
		f.pendingDeletes[id] = struct{}{}
		f.diskGarbage++
	}
}

func (f *flatFile) hasPending() bool {
	return len(f.pendingItems) > 0 || len(f.pendingDeletes) > 0
}

func (f *flatFile) needsCompact() bool {
	if f.legacyFormat {
		return true
	}
	if len(f.pendingDeletes) == 0 && f.diskGarbage == 0 {
		return false
	}
	total := f.diskRecords + len(f.pendingItems) + len(f.pendingDeletes)
	if total <= compactMinRecords {
		return f.diskGarbage > 0 || len(f.pendingDeletes) > 0
	}
	return float64(f.diskGarbage)/float64(total) >= compactGarbageFraction
}

// flush persists pending changes. allItems must return the full, current,
// authoritative item set; it is only invoked (lazily) when a full
// compacting rewrite is chosen, so callers that hold that set in a form
// requiring a conversion (e.g. HNSWStore's map) can defer the cost to when
// it's actually needed.
func (f *flatFile) flush(allItems func() []VectorItem) error {
	if !f.hasPending() {
		return nil
	}
	if f.needsCompact() {
		items := allItems()
		if err := writeFlatSnapshot(f.path, items); err != nil {
			return err
		}
		f.onDisk = make(map[string]struct{}, len(items))
		for _, it := range items {
			f.onDisk[it.ID] = struct{}{}
		}
		f.diskRecords = len(items)
		f.diskGarbage = 0
		f.legacyFormat = false
	} else {
		items := make([]VectorItem, 0, len(f.pendingItems))
		for _, it := range f.pendingItems {
			items = append(items, it)
		}
		deletes := make([]string, 0, len(f.pendingDeletes))
		for id := range f.pendingDeletes {
			deletes = append(deletes, id)
		}
		if err := appendFlatRecords(f.path, items, deletes); err != nil {
			return err
		}
		for _, it := range items {
			f.onDisk[it.ID] = struct{}{}
		}
		f.diskRecords += len(items) + len(deletes)
	}
	f.pendingItems = make(map[string]VectorItem)
	f.pendingDeletes = make(map[string]struct{})
	return nil
}

func readFlatRecords(r io.Reader) (items []VectorItem, recordCount int, err error) {
	m := make(map[string]VectorItem)
	order := make([]string, 0)
	for {
		var typ byte
		if err := binary.Read(r, binary.LittleEndian, &typ); err != nil {
			if err == io.EOF {
				break
			}
			return nil, 0, fmt.Errorf("read record type: %w", err)
		}
		var idLen uint32
		if err := binary.Read(r, binary.LittleEndian, &idLen); err != nil {
			return nil, 0, fmt.Errorf("read id len: %w", err)
		}
		idBytes := make([]byte, idLen)
		if _, err := io.ReadFull(r, idBytes); err != nil {
			return nil, 0, fmt.Errorf("read id: %w", err)
		}
		id := string(idBytes)
		recordCount++

		switch typ {
		case flatRecordData:
			var dimCount uint32
			if err := binary.Read(r, binary.LittleEndian, &dimCount); err != nil {
				return nil, 0, fmt.Errorf("read dim count: %w", err)
			}
			vec := make([]float32, dimCount)
			if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
				return nil, 0, fmt.Errorf("read vector: %w", err)
			}
			if _, exists := m[id]; !exists {
				order = append(order, id)
			}
			m[id] = VectorItem{ID: id, Vector: vec}
		case flatRecordTombstone:
			delete(m, id)
		default:
			return nil, 0, fmt.Errorf("unknown flat record type %d", typ)
		}
	}

	items = make([]VectorItem, 0, len(m))
	for _, id := range order {
		if it, ok := m[id]; ok {
			items = append(items, it)
		}
	}
	return items, recordCount, nil
}

// loadLegacyFlatVectors parses the pre-incremental format: a bare stream of
// data records (no header, no type byte, no tombstones), same as this
// package wrote before incremental persistence was added.
func loadLegacyFlatVectors(r io.Reader) ([]VectorItem, error) {
	var items []VectorItem
	for {
		var idLen uint32
		if err := binary.Read(r, binary.LittleEndian, &idLen); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read id len: %w", err)
		}
		idBytes := make([]byte, idLen)
		if _, err := io.ReadFull(r, idBytes); err != nil {
			return nil, fmt.Errorf("read id: %w", err)
		}
		var dimCount uint32
		if err := binary.Read(r, binary.LittleEndian, &dimCount); err != nil {
			return nil, fmt.Errorf("read dim count: %w", err)
		}
		vec := make([]float32, dimCount)
		if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
			return nil, fmt.Errorf("read vector: %w", err)
		}
		items = append(items, VectorItem{ID: string(idBytes), Vector: vec})
	}
	return items, nil
}

func writeDataRecord(w io.Writer, item VectorItem) error {
	if err := binary.Write(w, binary.LittleEndian, flatRecordData); err != nil {
		return err
	}
	idBytes := []byte(item.ID)
	if err := binary.Write(w, binary.LittleEndian, uint32(len(idBytes))); err != nil {
		return err
	}
	if _, err := w.Write(idBytes); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(item.Vector))); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, item.Vector)
}

func writeTombstoneRecord(w io.Writer, id string) error {
	if err := binary.Write(w, binary.LittleEndian, flatRecordTombstone); err != nil {
		return err
	}
	idBytes := []byte(id)
	if err := binary.Write(w, binary.LittleEndian, uint32(len(idBytes))); err != nil {
		return err
	}
	_, err := w.Write(idBytes)
	return err
}

// writeFlatSnapshot performs a full compacting rewrite: it atomically
// replaces path's contents with exactly items, in the current (new)
// format, via the tmp+rename pattern.
func writeFlatSnapshot(path string, items []VectorItem) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, flatMagic); err != nil {
		return fail(err)
	}
	if err := binary.Write(w, binary.LittleEndian, flatVersion); err != nil {
		return fail(err)
	}
	for _, item := range items {
		if err := writeDataRecord(w, item); err != nil {
			return fail(err)
		}
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// appendFlatRecords appends items (as data records) and deletes (as
// tombstone records) to the tail of path, writing the format header first
// if the file is new/empty. The write is fsync'd before returning so a
// crash can't leave a torn append.
func appendFlatRecords(path string, items []VectorItem, deletes []string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	w := bufio.NewWriterSize(f, 64<<10)
	if offset == 0 {
		if err := binary.Write(w, binary.LittleEndian, flatMagic); err != nil {
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, flatVersion); err != nil {
			return err
		}
	}
	for _, item := range items {
		if err := writeDataRecord(w, item); err != nil {
			return err
		}
	}
	for _, id := range deletes {
		if err := writeTombstoneRecord(w, id); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Sync()
}

func itemsToMap(items []VectorItem) map[string]VectorItem {
	m := make(map[string]VectorItem, len(items))
	for _, item := range items {
		m[item.ID] = item
	}
	return m
}

func mapToItems(m map[string]VectorItem) []VectorItem {
	items := make([]VectorItem, 0, len(m))
	for _, item := range m {
		items = append(items, item)
	}
	return items
}
