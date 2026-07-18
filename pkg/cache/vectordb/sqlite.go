package vectordb

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type sqliteStore struct {
	db     *sql.DB
	vec0   bool
	dim    int
	mu     sync.RWMutex
	logger *slog.Logger
	closed atomic.Bool
}

func newSQLiteStore(cfg *migrate.SQLiteVectorConfig, dim int, logger *slog.Logger) (*sqliteStore, error) {
	path := defaultSQLitePath()
	if cfg != nil && cfg.Path != "" {
		path = cfg.Path
	}

	if strings.ContainsAny(path, "?&") {
		return nil, fmt.Errorf("vectordb sqlite: path must not contain '?' or '&': %q", path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("vectordb sqlite: create dir: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("vectordb sqlite: open: %w", err)
	}

	s := &sqliteStore{
		db:     db,
		dim:    dim,
		logger: logger,
	}

	if err := s.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("vectordb sqlite: init: %w", err)
	}

	logger.Info("vectordb sqlite initialized", "path", path, "vec0", s.vec0)
	return s, nil
}

func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "leanproxy", "cache", "vectors.db")
	}
	return filepath.Join(home, ".leanproxy", "cache", "vectors.db")
}

func (s *sqliteStore) init() error {
	if err := s.tryVec0(); err != nil {
		s.logger.Warn("vectordb sqlite: vec0 extension not available, falling back to manual cosine search", "error", err)
		s.vec0 = false
	}

	if err := s.createTables(); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}

	return nil
}

func (s *sqliteStore) tryVec0() error {
	var ext sql.NullString
	err := s.db.QueryRow("SELECT load_extension('vec0')").Scan(&ext)
	if err != nil {
		var name string
		err2 := s.db.QueryRow("SELECT name FROM pragma_module_list WHERE name = 'vec0'").Scan(&name)
		if err2 != nil {
			return fmt.Errorf("vec0 not available: %w (lookup: %v)", err, err2)
		}
		return nil
	}
	s.vec0 = true
	return nil
}

func (s *sqliteStore) createTables() error {
	mainTable := `CREATE TABLE IF NOT EXISTS vectors (
		id TEXT PRIMARY KEY,
		vector BLOB NOT NULL,
		metadata TEXT DEFAULT '{}'
	)`
	if _, err := s.db.Exec(mainTable); err != nil {
		return fmt.Errorf("vectors table: %w", err)
	}

	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_vectors_id ON vectors(id)`); err != nil {
		return fmt.Errorf("vectors index: %w", err)
	}

	if s.vec0 {
		vecTable := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_vectors USING vec0(
			id TEXT PRIMARY KEY,
			vector float[%d]
		)`, s.dim)
		if _, err := s.db.Exec(vecTable); err != nil {
			s.logger.Warn("vectordb sqlite: vec0 table creation failed, using manual search", "error", err)
			s.vec0 = false
		}
	}

	return nil
}

func (s *sqliteStore) Upsert(ctx context.Context, records ...VectorRecord) error {
	if len(records) == 0 {
		return nil
	}

	if s.closed.Load() {
		return fmt.Errorf("vectordb sqlite: store closed")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO vectors (id, vector, metadata) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, rec := range records {
		vecBytes := float32SliceToBytes(rec.Vector)
		metaBytes := marshalMetadata(rec.Metadata)
		if _, err := stmt.ExecContext(ctx, rec.ID, vecBytes, string(metaBytes)); err != nil {
			return fmt.Errorf("upsert %q: %w", rec.ID, err)
		}
	}

	if s.vec0 {
		vecStmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO vec_vectors (id, vector) VALUES (?, ?)`)
		if err != nil {
			return fmt.Errorf("vec0 prepare: %w", err)
		}
		defer vecStmt.Close()

		for _, rec := range records {
			floatStr := float32SliceToString(rec.Vector)
			if _, err := vecStmt.ExecContext(ctx, rec.ID, floatStr); err != nil {
				return fmt.Errorf("vec0 upsert %q: %w", rec.ID, err)
			}
		}
	}

	return tx.Commit()
}

func (s *sqliteStore) Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		k = 10
	}

	if s.closed.Load() {
		return nil, fmt.Errorf("vectordb sqlite: store closed")
	}

	if s.vec0 {
		return s.searchVec0(ctx, vector, k)
	}
	return s.searchManual(ctx, vector, k)
}

func (s *sqliteStore) searchVec0(ctx context.Context, vector []float32, k int) ([]SearchResult, error) {
	floatStr := float32SliceToString(vector)
	query := `SELECT id, distance FROM vec_vectors WHERE vector MATCH ? ORDER BY distance LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, floatStr, k)
	if err != nil {
		return nil, fmt.Errorf("vec0 search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var id string
		var distance float64
		if err := rows.Scan(&id, &distance); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		rec, err := s.getRecord(ctx, id)
		if err != nil {
			s.logger.Warn("vectordb sqlite: vec0 result not in vectors table", "id", id, "error", err)
			continue
		}

		results = append(results, SearchResult{
			Record: rec,
			Score:  1.0 - distance,
		})
	}

	return results, rows.Err()
}

func (s *sqliteStore) searchManual(ctx context.Context, vector []float32, k int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, vector, metadata FROM vectors`)
	if err != nil {
		return nil, fmt.Errorf("manual search query: %w", err)
	}
	defer rows.Close()

	var candidates []SearchResult
	for rows.Next() {
		var id string
		var vecBytes []byte
		var metaStr sql.NullString
		if err := rows.Scan(&id, &vecBytes, &metaStr); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		storedVec := bytesToFloat32Slice(vecBytes)
		score := cosineSimilarity(vector, storedVec)
		if storedVec == nil {
			continue
		}

		candidates = append(candidates, SearchResult{
			Record: VectorRecord{
				ID:       id,
				Vector:   storedVec,
				Metadata: unmarshalMetadata([]byte(metaStr.String)),
			},
			Score: score,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	sortResults(candidates)

	if k > len(candidates) {
		k = len(candidates)
	}
	return candidates[:k], nil
}

func (s *sqliteStore) getRecord(ctx context.Context, id string) (VectorRecord, error) {
	var rec VectorRecord
	var vecBytes []byte
	var metaStr sql.NullString

	err := s.db.QueryRowContext(ctx, `SELECT id, vector, metadata FROM vectors WHERE id = ?`, id).Scan(&rec.ID, &vecBytes, &metaStr)
	if err != nil {
		return rec, fmt.Errorf("get record %q: %w", id, err)
	}

	rec.Vector = bytesToFloat32Slice(vecBytes)
	rec.Metadata = unmarshalMetadata([]byte(metaStr.String))
	return rec, nil
}

func (s *sqliteStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	if s.closed.Load() {
		return fmt.Errorf("vectordb sqlite: store closed")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM vectors WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("delete %q: %w", id, err)
		}
	}

	if s.vec0 {
		vecStmt, err := tx.PrepareContext(ctx, `DELETE FROM vec_vectors WHERE id = ?`)
		if err != nil {
			return fmt.Errorf("vec0 prepare delete: %w", err)
		}
		defer vecStmt.Close()

		for _, id := range ids {
			if _, err := vecStmt.ExecContext(ctx, id); err != nil {
				s.logger.Warn("vectordb sqlite: vec0 delete failed", "id", id, "error", err)
			}
		}
	}

	return tx.Commit()
}

func (s *sqliteStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	return s.db.Close()
}

func float32SliceToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func bytesToFloat32Slice(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func float32SliceToString(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fmt.Sprintf("%f", f))
	}
	b.WriteByte(']')
	return b.String()
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func sortResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

func marshalMetadata(m map[string]string) []byte {
	if m == nil {
		return []byte("{}")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func unmarshalMetadata(data []byte) map[string]string {
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return make(map[string]string)
	}
	return m
}
