package vectorstore

import (
	"bufio"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeLegacyFlatFile writes the pre-incremental (headerless, no record
// type byte, no tombstones) format directly, bypassing this package's
// current (post-migration) writer, so we can test that old on-disk files
// left over from before incremental persistence still load correctly.
func writeLegacyFlatFile(t *testing.T, path string, items []VectorItem) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, item := range items {
		idBytes := []byte(item.ID)
		if err := binary.Write(w, binary.LittleEndian, uint32(len(idBytes))); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(idBytes); err != nil {
			t.Fatal(err)
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(item.Vector))); err != nil {
			t.Fatal(err)
		}
		if err := binary.Write(w, binary.LittleEndian, item.Vector); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadOldFormatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	want := []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0, 1, 0}},
		{ID: "3", Vector: []float32{0, 0, 1}},
	}
	writeLegacyFlatFile(t, path, want)

	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Count() != len(want) {
		t.Fatalf("expected %d items loaded from old-format file, got %d", len(want), store.Count())
	}
	ids := store.IDs()
	if !sameStringSet(ids, []string{"1", "2", "3"}) {
		t.Fatalf("expected IDs [1 2 3], got %v", ids)
	}

	results, err := store.Search(context.Background(), []float32{0, 1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "2" {
		t.Fatalf("expected top result ID 2, got %+v", results)
	}
}

func TestLoadOldFormatFileThenAppendRoundtrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	writeLegacyFlatFile(t, path, []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0, 1, 0}},
	})

	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Add(ctx, []VectorItem{{ID: "3", Vector: []float32{0, 0, 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	// The migration flush must have upgraded the file to the new format
	// (the old writer never wrote a header).
	if !fileStartsWithFlatMagic(t, path) {
		t.Fatal("expected file to be migrated to the new format on first flush")
	}

	reloaded, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(reloaded.IDs(), []string{"1", "2", "3"}) {
		t.Fatalf("expected IDs [1 2 3] after migrate+reload, got %v", reloaded.IDs())
	}
}

func fileStartsWithFlatMagic(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := f.Read(head); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint32(head) == flatMagic
}

func TestAppendThenLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// First flush: nothing on disk yet, must go through the append path
	// and lay down the header.
	if err := store.Add(ctx, []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0, 1, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	sizeAfterFirstFlush, err := fileSize(path)
	if err != nil {
		t.Fatal(err)
	}

	// Second flush: a pure add, should be a cheap append (file grows by
	// roughly one record, not rewritten from scratch).
	if err := store.Add(ctx, []VectorItem{
		{ID: "3", Vector: []float32{0, 0, 1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	sizeAfterSecondFlush, err := fileSize(path)
	if err != nil {
		t.Fatal(err)
	}
	if sizeAfterSecondFlush <= sizeAfterFirstFlush {
		t.Fatalf("expected file to grow after appending a new item: %d -> %d", sizeAfterFirstFlush, sizeAfterSecondFlush)
	}

	// An update to an existing item should also append (not rewrite),
	// since no deletes have happened yet and we're well under the
	// compaction threshold.
	if err := store.Add(ctx, []VectorItem{
		{ID: "1", Vector: []float32{0.5, 0.5, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 3 {
		t.Fatalf("expected 3 items after append-roundtrip, got %d", reloaded.Count())
	}
	results, err := reloaded.Search(ctx, []float32{0.5, 0.5, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "1" {
		t.Fatalf("expected updated vector for ID 1 to win nearest search, got %+v", results)
	}
}

func TestFlushIsNoOpWhenNothingMutated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(context.Background(), []VectorItem{{ID: "1", Vector: []float32{1, 0, 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	sizeBefore, err := fileSize(path)
	if err != nil {
		t.Fatal(err)
	}
	// Flush again with no intervening mutation: must be a true no-op (no
	// write at all), which readers rely on to not clobber a concurrent
	// writer's file.
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	sizeAfter, err := fileSize(path)
	if err != nil {
		t.Fatal(err)
	}
	if sizeBefore != sizeAfter {
		t.Fatalf("expected no-op flush to leave file size unchanged: %d -> %d", sizeBefore, sizeAfter)
	}
}

func TestCompactionReclaimsDeletedSpace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const n = 20
	items := make([]VectorItem, n)
	for i := range items {
		items[i] = VectorItem{ID: string(rune('a' + i)), Vector: []float32{float32(i), 1, 0}}
	}
	if err := store.Add(ctx, items); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	// Delete most of the items: garbage fraction blows past the
	// compaction threshold, so this flush must compact (file should
	// shrink back down toward just the survivors, not keep growing).
	var toDelete []string
	for i := 0; i < n-2; i++ {
		toDelete = append(toDelete, items[i].ID)
	}
	if err := store.Delete(ctx, toDelete); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 2 {
		t.Fatalf("expected 2 survivors after mass delete, got %d", reloaded.Count())
	}
}

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
