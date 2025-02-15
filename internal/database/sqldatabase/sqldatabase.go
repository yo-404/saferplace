package sqldatabase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"api.safer.place/incident/v1"
	"api.safer.place/viewer/v1"

	"github.com/google/uuid"
	"golang.org/x/exp/slices"
	"google.golang.org/protobuf/types/known/timestamppb"
	"safer.place/internal/database"
)

// Config of the SQLDatabase
type Config struct {
	Driver string `yaml:"driver" default:"sqlite3"`
	DSN    string `yaml:"dsn" default:"file:incidents.db"`
}

// Database contains the database connection
type Database struct {
	db *sql.DB

	hasIncidentStmt            *sql.Stmt
	saveIncidentStmt           *sql.Stmt
	updateResolutionStmt       *sql.Stmt
	saveCommentStmt            *sql.Stmt
	viewIncidentStmt           *sql.Stmt
	viewCommentsStmt           *sql.Stmt
	incidentsWithoutReviewStmt *sql.Stmt
	incidentsInRadiusStmt      *sql.Stmt
	incidentsInRegionStmt      *sql.Stmt
	saveSessionStmt            *sql.Stmt
	isValidSessionStmt         *sql.Stmt
	alertingIncidentsStmt      *sql.Stmt
}

// New creates a new SQL database
func New(cfg Config) (*Database, error) {
	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	// TODO: Probably uncomment to allow migrations
	if _, err := db.Exec(createTableQuery); err != nil {
		return nil, fmt.Errorf("unable to prepare database: %w", err)
	}

	hasIncidentStmt, err := db.Prepare("SELECT id FROM incidents WHERE id=?")
	if err != nil {
		return nil, fmt.Errorf("unable to prepare hasIncidents query: %w", err)
	}
	saveIncidentStmt, err := db.Prepare(saveIncidentQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare saveIncident query: %w", err)
	}
	updateResolutionStmt, err := db.Prepare(updateResolutionQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare updateResolution query: %w", err)
	}
	saveCommentStmt, err := db.Prepare(saveCommentQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare saveComment query: %w", err)
	}
	viewIncidentStmt, err := db.Prepare(viewIncidentQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare viewIncident query: %w", err)
	}
	viewCommentsStmt, err := db.Prepare(viewCommentsQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare viewComments query: %w", err)
	}
	incidentsWithoutReviewStmt, err := db.Prepare(incidentsWithoutReviewQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare incidentsWithoutReview query: %w", err)
	}
	incidentsInRadiusStmt, err := db.Prepare(incidentsInRadiusQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare incidentsInRadius query: %w", err)
	}
	incidentsInRegionStmt, err := db.Prepare(incidentsInRegionQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare incidentsInRegion query: %w", err)
	}
	saveSessionStmt, err := db.Prepare(saveSessionQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare saveComment query: %w", err)
	}
	isValidSessionStmt, err := db.Prepare(isValidSessionQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare isValidSession query: %w", err)
	}
	alertingIncidentsStmt, err := db.Prepare(alertingIncidentsQuery)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare isValidSession query: %w", err)
	}

	return &Database{
		db:                         db,
		hasIncidentStmt:            hasIncidentStmt,
		saveIncidentStmt:           saveIncidentStmt,
		updateResolutionStmt:       updateResolutionStmt,
		saveCommentStmt:            saveCommentStmt,
		viewIncidentStmt:           viewIncidentStmt,
		viewCommentsStmt:           viewCommentsStmt,
		incidentsWithoutReviewStmt: incidentsWithoutReviewStmt,
		incidentsInRadiusStmt:      incidentsInRadiusStmt,
		saveSessionStmt:            saveSessionStmt,
		isValidSessionStmt:         isValidSessionStmt,
		alertingIncidentsStmt:      alertingIncidentsStmt,
		incidentsInRegionStmt:      incidentsInRegionStmt,
	}, nil
}

// SaveIncident to the sql database
func (db *Database) SaveIncident(ctx context.Context, inc *incident.Incident) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("unable to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if exists, err := db.hasIncident(ctx, tx, inc.Id); err != nil {
		return fmt.Errorf("unable to check does the incident exist: %w", err)
	} else if exists {
		return database.ErrAlreadyExists
	}

	if _, err := tx.Stmt(db.saveIncidentStmt).ExecContext(ctx,
		inc.Id,
		inc.Timestamp.Seconds,
		inc.Description,
		inc.Coordinates.Lat,
		inc.Coordinates.Lon,
		inc.Resolution.String(),
		inc.ImageId,
	); err != nil {
		return fmt.Errorf("unable to save incident: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit transaction: %w", err)
	}

	return nil
}

// SaveReview updates the incident record with the resolution and adds a comment.
func (db *Database) SaveReview(
	ctx context.Context,
	id string,
	res incident.Resolution,
	comment *incident.Comment,
) error {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("unable to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if exists, err := db.hasIncident(ctx, tx, id); err != nil {
		return fmt.Errorf("unable to check does the incident exist: %w", err)
	} else if !exists {
		return database.ErrDoesNotExist
	}

	if _, err := tx.Stmt(db.updateResolutionStmt).ExecContext(
		ctx, res.String(), id,
	); err != nil {
		return fmt.Errorf("unable to update incident resolution: %w", err)
	}

	if _, err := tx.Stmt(db.saveCommentStmt).ExecContext(
		ctx,
		uuid.New().String(), // id
		id,                  // incident_id
		comment.Timestamp,   // timestamp
		comment.AuthorId,    // author
		comment.Message,     // comment
		res.String(),        // resolution

	); err != nil {
		return fmt.Errorf("unable to save comment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("unable to commit transaction: %w", err)
	}

	return nil
}

// ViewIncident recovers incident information
func (db *Database) ViewIncident(ctx context.Context, id string) (*incident.Incident, error) {
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inc, err := scanIncident(tx.Stmt(db.viewIncidentStmt).QueryRow(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, database.ErrDoesNotExist
		}
		return nil, fmt.Errorf("unable to get incident info: %w", err)
	}

	// TODO: Get comments
	rows, err := tx.Stmt(db.viewCommentsStmt).QueryContext(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unable to get incident comments: %w", err)
	}
	for rows.Next() {
		comment := new(incident.Comment)
		discard := ""
		if err := rows.Scan(
			&discard,
			&discard,
			&comment.Timestamp, // timestamp
			&comment.AuthorId,  // author
			&comment.Message,   // comment
			&discard,           // resolution, TODO: Maybe add to comment?
		); err != nil {
			return nil, fmt.Errorf("unable to scan comment: %w", err)
		}
		inc.ReviewerComments = append(inc.ReviewerComments, comment)
	}

	// Sort the reviewer comments
	sort.Sort(ByTimestamp(inc.ReviewerComments))

	// We don't actually change anything but we are using this to close the
	// transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("unable to commit transaction: %w", err)
	}
	return inc, nil
}

// IncidentsWithoutReview gets all the incidents which have the UNDEFINED
func (db *Database) IncidentsWithoutReview(ctx context.Context) ([]*incident.Incident, error) {
	rows, err := db.incidentsWithoutReviewStmt.QueryContext(ctx,
		incident.Resolution_RESOLUTION_UNSPECIFIED.String(),
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []*incident.Incident{}, nil
		}
		return nil, fmt.Errorf("unable list incidents: %w", err)
	}

	incidents := make([]*incident.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, database.ErrDoesNotExist
			}
			return nil, fmt.Errorf("unable to get incident info: %w", err)
		}
		incidents = append(incidents, inc)
	}

	return incidents, nil
}

// IncidentsInRadius gets all incidents and then does some maths to filter it to only include
// incidents in the provided radius
func (db *Database) IncidentsInRadius(
	ctx context.Context, center *incident.Coordinates, radius float64,
) ([]*incident.Incident, error) {
	rows, err := db.incidentsInRadiusStmt.QueryContext(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []*incident.Incident{}, nil
		}
		return nil, fmt.Errorf("unable list incidents: %w", err)
	}

	incidents := make([]*incident.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, database.ErrDoesNotExist
			}
			return nil, fmt.Errorf("unable to get incident info: %w", err)
		}
		incidents = append(incidents, inc)
	}

	// Delete all incidents which are outside of the given radius
	incidents = slices.DeleteFunc(incidents, func(i *incident.Incident) bool {
		return distance(center.Lat, center.Lon, i.Coordinates.Lat, i.Coordinates.Lon) > radius
	})

	return incidents, nil
}

// IncidentsInRegion returns all the incidents in the specified region
func (db *Database) IncidentsInRegion(
	ctx context.Context, since time.Time, region *viewer.Region,
) ([]*incident.Incident, error) {
	rows, err := db.incidentsInRegionStmt.QueryContext(ctx,
		since.Unix(),
		region.North/100,
		region.South/100,
		region.West/100,
		region.East/100,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []*incident.Incident{}, nil
		}
		return nil, fmt.Errorf("unable list incidents: %w", err)
	}

	incidents := make([]*incident.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, database.ErrDoesNotExist
			}
			return nil, fmt.Errorf("unable to get incident info: %w", err)
		}
		incidents = append(incidents, inc)
	}

	return incidents, nil
}

// SaveSession in the database
// TODO: Decide should the database layer decide on the session expiry or should it be
// determined somewhere else.
func (db *Database) SaveSession(ctx context.Context, session string) error {
	// TODO: At least make the expiry configurable.
	expiry := time.Now().Add(1 * time.Hour)
	if _, err := db.saveSessionStmt.ExecContext(ctx, session, expiry.Unix()); err != nil {
		return fmt.Errorf("unable to save session: %w", err)
	}

	return nil
}

// AlertingIncidents returns the incidents which are alerting and match the filters
func (db *Database) AlertingIncidents(
	ctx context.Context, since time.Time, region *viewer.Region,
) ([]*incident.Incident, error) {
	rows, err := db.alertingIncidentsStmt.QueryContext(ctx,
		since.Unix(),
		region.North/100,
		region.South/100,
		region.West/100,
		region.East/100,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []*incident.Incident{}, nil
		}
		return nil, fmt.Errorf("unable list incidents: %w", err)
	}

	incidents := make([]*incident.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, database.ErrDoesNotExist
			}
			return nil, fmt.Errorf("unable to get incident info: %w", err)
		}
		incidents = append(incidents, inc)
	}

	return incidents, nil
}

// IsValidSession determines if the session is still active and within date.
// It returns nil if the session is valid, otherwise some error.
// TODO: If the session is expired, delete it
func (db *Database) IsValidSession(ctx context.Context, session string) error {
	row := db.isValidSessionStmt.QueryRowContext(ctx, session)
	if err := row.Err(); err != nil {
		return fmt.Errorf("unable to check if the session is valid: %w", err)
	}
	var expiryUnix int64
	if err := row.Scan(&expiryUnix); err != nil {
		return fmt.Errorf("unable to scan row: %w", err)
	}

	expiry := time.Unix(expiryUnix, 0)
	if time.Since(expiry) > 0 {
		return errors.New("session expired")
	}

	return nil
}

func (db *Database) hasIncident(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	// First check do we already have an entry, so we can return already exists
	row := tx.Stmt(db.hasIncidentStmt).QueryRowContext(ctx, id)

	var existingID string
	if err := row.Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("unable to check for existance of incident: %w", err)
	}

	return id == existingID, nil
}

// ByTimestamp sorts the comments by timestamp, from the oldest to the newest
type ByTimestamp []*incident.Comment

func (s ByTimestamp) Len() int           { return len(s) }
func (s ByTimestamp) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ByTimestamp) Less(i, j int) bool { return s[i].Timestamp < s[j].Timestamp }

type scanner interface {
	Scan(dest ...any) error
}

func scanIncident(s scanner) (*incident.Incident, error) {
	inc := &incident.Incident{Coordinates: &incident.Coordinates{}}
	var resolution string
	var timestamp int64
	if err := s.Scan(
		&inc.Id,
		&timestamp,
		&inc.Description,
		&inc.Coordinates.Lat,
		&inc.Coordinates.Lon,
		&resolution,
		&inc.ImageId,
	); err != nil {
		return nil, err
	}
	inc.Resolution = incident.Resolution(incident.Resolution_value[resolution])
	inc.Timestamp = &timestamppb.Timestamp{Seconds: timestamp}

	return inc, nil
}

var createTableQuery = `
CREATE TABLE IF NOT EXISTS incidents (
	id TEXT PRIMARY KEY,
	timestamp INTEGER NOT NULL,
	description TEXT,
	lat REAL NOT NULL,
	lon REAL NOT NULL,
	resolution TEXT NOT NULL,
	image TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS lat ON incidents (lat);
CREATE INDEX IF NOT EXISTS lon ON incidents (lon);

CREATE TABLE IF NOT EXISTS comments (
	id TEXT PRIMARY KEY,
	incident_id TEXT NOT NULL,
	timestamp INTEGER NOT NULL,
	author TEXT NOT NULL,
	comment TEXT NOT NULL,
	resolution TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS incident_ids ON comments (incident_id);

CREATE TABLE IF NOT EXISTS sessions (
	id     TEXT PRIMARY KEY,
	expiry INTEGER NOT NULL
);
`

var saveIncidentQuery = `
INSERT INTO incidents
	(id, timestamp, description, lat, lon, resolution, image)
VALUES
	(?, ?, ?, ?, ?, ?, ?);
`

var updateResolutionQuery = `
UPDATE incidents
SET
	resolution=?
WHERE
	id=?;
`

var saveCommentQuery = `
INSERT INTO comments
	(id, incident_id, timestamp, author, comment, resolution)
VALUES
	(?, ?, ?, ?, ?, ?);
`

var viewIncidentQuery = `
SELECT * FROM incidents WHERE id=?;
`

var viewCommentsQuery = `
SELECT * FROM comments WHERE incident_id=?;
`

var incidentsWithoutReviewQuery = `
SELECT * FROM incidents WHERE resolution=?;
`

// incidentsInRadiusQuery gets all incidents as some SQL databases might not contain geospatial functions
// We might have to look into altenative databases for more efficient querying.
var incidentsInRadiusQuery = fmt.Sprintf(`
SELECT *
FROM incidents
WHERE
	resolution=%q
	OR
	resolution=%q;
`,
	incident.Resolution_RESOLUTION_ACCEPTED,
	incident.Resolution_RESOLUTION_ALERTED,
)

var saveSessionQuery = `
INSERT INTO sessions
	(id, expiry)
VALUES
	(?, ?);
`

var isValidSessionQuery = `
SELECT expiry FROM sessions WHERE id=?;
`

// incidentsInRegionQuery gets only incidents since the provided timestamp,
// in the provided region
// parameters:
//
//	since
//	north
//	south
//	west
//	east
var incidentsInRegionQuery = fmt.Sprintf(`
SELECT *
FROM incidents
WHERE
	(resolution=%q OR resolution=%q)
	AND
		timestamp > ?
	AND
		lat < ?
	AND
		lat > ?
	AND
		lon > ?
	AND
		lon < ?
`,
	incident.Resolution_RESOLUTION_ACCEPTED,
	incident.Resolution_RESOLUTION_ALERTED,
)

// alertingIncidentsQuery gets only incidents since the provided timestamp,
// in the provided region
// parameters:
//
//	since
//	north
//	south
//	west
//	east
var alertingIncidentsQuery = fmt.Sprintf(`
SELECT *
FROM incidents
WHERE
	resolution=%q
	AND
		timestamp > ?
	AND
		lat < ?
	AND
		lat > ?
	AND
		lon > ?
	AND
		lon < ?
`,
	incident.Resolution_RESOLUTION_ALERTED,
)
