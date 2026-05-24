package service

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	h3 "github.com/uber/h3-go/v4"
	_ "modernc.org/sqlite"
)

// CoverageDB stores all received positions in SQLite with H3 indexing
// for efficient hexagon-based aggregation at multiple zoom levels.
//
// H3 resolutions used:
//
//	r9 → ~174m hexagons (street-level zoom)
//	r7 → ~1.2km hexagons (city-level zoom)
//	r5 → ~8.5km hexagons (region-level zoom)
type CoverageDB struct {
	db     *sql.DB
	logger *log.Logger
	mu     sync.RWMutex
}

const coverageDBPath = "data/coverage.db"

func newCoverageDB(logger *log.Logger) (*CoverageDB, error) {
	if err := os.MkdirAll(filepath.Dir(coverageDBPath), 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	db, err := sql.Open("sqlite", coverageDBPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	schema := `
CREATE TABLE IF NOT EXISTS samples (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    issi     INTEGER NOT NULL,
    lat      REAL NOT NULL,
    lon      REAL NOT NULL,
    rssi     INTEGER,
    snr      INTEGER,
    h9       INTEGER NOT NULL,
    h7       INTEGER NOT NULL,
    h5       INTEGER NOT NULL,
    ts       INTEGER NOT NULL,
    repeater TEXT
);
CREATE INDEX IF NOT EXISTS idx_samples_h5 ON samples(h5);
CREATE INDEX IF NOT EXISTS idx_samples_h7 ON samples(h7);
CREATE INDEX IF NOT EXISTS idx_samples_h9 ON samples(h9);
CREATE INDEX IF NOT EXISTS idx_samples_ts ON samples(ts);
CREATE INDEX IF NOT EXISTS idx_samples_issi ON samples(issi);
`
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	// Migrate existing installs (ignore error if column already exists).
	_, _ = db.Exec(`ALTER TABLE samples ADD COLUMN repeater TEXT`)

	logger.Printf("CoverageDB: opened %s", coverageDBPath)
	return &CoverageDB{db: db, logger: logger}, nil
}

// Insert adds a position sample. `tmoSite` is the callsign of the TMO-site
// that heard it (may be empty for anonymous / unknown sources).
func (cdb *CoverageDB) Insert(issi uint32, lat, lon float64, rssi, snr *int, tmoSite string) error {
	cdb.mu.Lock()
	defer cdb.mu.Unlock()

	latLng := h3.NewLatLng(lat, lon)
	h9, err := h3.LatLngToCell(latLng, 9)
	if err != nil {
		return err
	}
	h7, err := h3.LatLngToCell(latLng, 7)
	if err != nil {
		return err
	}
	h5, err := h3.LatLngToCell(latLng, 5)
	if err != nil {
		return err
	}

	var rssiVal, snrVal sql.NullInt64
	if rssi != nil {
		rssiVal = sql.NullInt64{Int64: int64(*rssi), Valid: true}
	}
	if snr != nil {
		snrVal = sql.NullInt64{Int64: int64(*snr), Valid: true}
	}

	var rep sql.NullString
	if tmoSite != "" {
		rep = sql.NullString{String: tmoSite, Valid: true}
	}

	_, err = cdb.db.Exec(
		`INSERT INTO samples (issi, lat, lon, rssi, snr, h9, h7, h5, ts, repeater) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issi, lat, lon, rssiVal, snrVal, int64(h9), int64(h7), int64(h5), time.Now().Unix(), rep,
	)
	return err
}

// HexAggregation is one hexagon with sample count and (optional) avg RSSI.
type HexAggregation struct {
	HexID      string   `json:"h"`
	Lat        float64  `json:"lat"`
	Lon        float64  `json:"lon"`
	Count      int      `json:"n"`
	AvgRSSI    *int     `json:"rssi,omitempty"`
	IssiSet    int      `json:"u"` // unique ISSIs
	Resolution int      `json:"r"`
	LastTs     int64    `json:"t"` // most recent sample, unix seconds
	TMOSites   []string `json:"rp,omitempty"`
}

// AggregateHexes returns hexagon aggregates for the given resolution.
// Optional bounding box filter (lat/lon min/max).
// `sinceTs` (Unix-Sekunden, 0 = kein Filter) schliesst aeltere Samples aus —
// damit die Map nicht von uralten Eintraegen verschmutzt wird.
func (cdb *CoverageDB) AggregateHexes(resolution int, minLat, minLon, maxLat, maxLon *float64, sinceTs int64) ([]HexAggregation, error) {
	cdb.mu.RLock()
	defer cdb.mu.RUnlock()

	hexCol := "h9"
	switch resolution {
	case 5:
		hexCol = "h5"
	case 7:
		hexCol = "h7"
	case 9:
		hexCol = "h9"
	default:
		return nil, fmt.Errorf("unsupported resolution %d (use 5, 7, or 9)", resolution)
	}

	query := fmt.Sprintf(`
SELECT %s, COUNT(*) as n, COUNT(DISTINCT issi) as u, AVG(rssi) as avg_rssi,
       MAX(ts) as last_ts, GROUP_CONCAT(DISTINCT repeater) as reps
FROM samples
`, hexCol)

	args := []any{}
	whereParts := []string{}
	if minLat != nil && minLon != nil && maxLat != nil && maxLon != nil {
		whereParts = append(whereParts, "lat BETWEEN ? AND ? AND lon BETWEEN ? AND ?")
		args = append(args, *minLat, *maxLat, *minLon, *maxLon)
	}
	if sinceTs > 0 {
		whereParts = append(whereParts, "ts >= ?")
		args = append(args, sinceTs)
	}
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}

	query += fmt.Sprintf(` GROUP BY %s LIMIT 10000`, hexCol)

	rows, err := cdb.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]HexAggregation, 0, 100)
	for rows.Next() {
		var hexID int64
		var count, unique int
		var avgRSSI sql.NullFloat64
		var lastTs sql.NullInt64
		var reps sql.NullString
		if err := rows.Scan(&hexID, &count, &unique, &avgRSSI, &lastTs, &reps); err != nil {
			continue
		}

		cell := h3.Cell(hexID)
		latLng, err := cell.LatLng()
		if err != nil {
			continue
		}

		agg := HexAggregation{
			HexID:      cell.String(),
			Lat:        latLng.Lat,
			Lon:        latLng.Lng,
			Count:      count,
			IssiSet:    unique,
			Resolution: resolution,
		}
		if avgRSSI.Valid {
			r := int(avgRSSI.Float64)
			agg.AvgRSSI = &r
		}
		if lastTs.Valid {
			agg.LastTs = lastTs.Int64
		}
		if reps.Valid && reps.String != "" {
			for _, r := range strings.Split(reps.String, ",") {
				if r = strings.TrimSpace(r); r != "" {
					agg.TMOSites = append(agg.TMOSites, r)
				}
			}
		}
		out = append(out, agg)
	}
	return out, nil
}

// RecentSamples returns the most recent N samples (for high-zoom point view).
func (cdb *CoverageDB) RecentSamples(limit int) ([]Position, error) {
	cdb.mu.RLock()
	defer cdb.mu.RUnlock()

	rows, err := cdb.db.Query(
		`SELECT issi, lat, lon, ts FROM samples ORDER BY ts DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Position, 0, limit)
	for rows.Next() {
		var issi uint32
		var lat, lon float64
		var ts int64
		if err := rows.Scan(&issi, &lat, &lon, &ts); err != nil {
			continue
		}
		out = append(out, Position{
			ISSI:      issi,
			Lat:       lat,
			Lon:       lon,
			Timestamp: time.Unix(ts, 0),
		})
	}
	return out, nil
}

// Stats returns total counts.
func (cdb *CoverageDB) Stats() (totalSamples, uniqueIssis int) {
	cdb.mu.RLock()
	defer cdb.mu.RUnlock()
	cdb.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT issi) FROM samples`).Scan(&totalSamples, &uniqueIssis)
	return
}

// Devices24h returns the number of distinct ISSIs seen in the last 24 hours.
func (cdb *CoverageDB) Devices24h() int {
	cdb.mu.RLock()
	defer cdb.mu.RUnlock()
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	var n int
	cdb.db.QueryRow(`SELECT COUNT(DISTINCT issi) FROM samples WHERE ts >= ?`, cutoff).Scan(&n)
	return n
}

func (cdb *CoverageDB) Close() error {
	if cdb.db != nil {
		return cdb.db.Close()
	}
	return nil
}
