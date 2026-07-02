package vectorstore

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

func loadFlatVectors(path string) ([]VectorItem, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)

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

func saveFlatVectors(path string, items []VectorItem) error {
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
	for _, item := range items {
		idBytes := []byte(item.ID)
		if err := binary.Write(w, binary.LittleEndian, uint32(len(idBytes))); err != nil {
			return fail(err)
		}
		if _, err := w.Write(idBytes); err != nil {
			return fail(err)
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(item.Vector))); err != nil {
			return fail(err)
		}
		if err := binary.Write(w, binary.LittleEndian, item.Vector); err != nil {
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
