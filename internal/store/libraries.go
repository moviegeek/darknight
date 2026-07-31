package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/moviegeek/darknight/internal/model"
)

// ErrNotFound is returned when a single-row lookup misses.
var ErrNotFound = errors.New("not found")

// CreateLibrary inserts a new library root and returns it with its id.
func (s *Store) CreateLibrary(ctx context.Context, name, rootPath string, scanInterval int) (*model.Library, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `
INSERT INTO libraries(name, root_path, scan_interval, last_scan_at, created_at, updated_at)
VALUES (?, ?, ?, 0, ?, ?)`,
		name, rootPath, scanInterval, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &model.Library{
		ID: id, Name: name, RootPath: rootPath,
		ScanInterval: scanInterval, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// ListLibraries returns all configured libraries.
func (s *Store) ListLibraries(ctx context.Context) ([]model.Library, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, name, root_path, scan_interval, last_scan_at, created_at, updated_at
FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Library{}
	for rows.Next() {
		var l model.Library
		if err := rows.Scan(&l.ID, &l.Name, &l.RootPath, &l.ScanInterval,
			&l.LastScanAt, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLibrary fetches a single library by id.
func (s *Store) GetLibrary(ctx context.Context, id int64) (*model.Library, error) {
	var l model.Library
	err := s.DB.QueryRowContext(ctx, `
SELECT id, name, root_path, scan_interval, last_scan_at, created_at, updated_at
FROM libraries WHERE id = ?`, id).Scan(
		&l.ID, &l.Name, &l.RootPath, &l.ScanInterval,
		&l.LastScanAt, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// DeleteLibrary removes a library and cascades to its movie_files.
func (s *Store) DeleteLibrary(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	return err
}

// TouchLibraryScan records when a scan of this library finished.
func (s *Store) TouchLibraryScan(ctx context.Context, id int64, at int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE libraries SET last_scan_at = ?, updated_at = ? WHERE id = ?`,
		at, at, id)
	return err
}
