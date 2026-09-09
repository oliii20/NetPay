package csvwrite

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAllToCSV writes both headers and rows into the csv file in the given path.
// The csv file will be closed after being written.
func WriteAllToCSV(path string, header []string, rows [][]string) error {
	f, err := createFileWithDirs(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	defer func() { _ = f.Close() }()

	return writeCSV(f, header, rows)
}

// WriteAllToCSVReplace atomically replaces path with a complete CSV snapshot.
func WriteAllToCSVReplace(path string, header []string, rows [][]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := f.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err = writeCSV(f, header, rows); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to replace csv file: %w", err)
	}

	return nil
}

func writeCSV(f *os.File, header []string, rows [][]string) error {
	w := csv.NewWriter(f)

	if err := w.Write(header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return fmt.Errorf("failed to write row: %w", err)
		}
	}

	w.Flush()

	return w.Error()
}

func createFileWithDirs(path string) (*os.File, error) {
	// create the directory
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	// create the file
	// if the file exists, return error
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("file already exists: %s", path)
		}

		return nil, err
	}

	return f, nil
}
