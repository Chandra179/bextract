package store

import (
	"context"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/arangodb/go-driver/v2/connection"
	"github.com/google/uuid"
)

const collectionName = "extractions"

// ArangoStore implements Store using ArangoDB.
type ArangoStore struct {
	col arangodb.Collection
}

// NewArangoStore connects to ArangoDB, auto-creates the database and collection if missing,
// and returns a ready-to-use ArangoStore.
func NewArangoStore(ctx context.Context, host, dbName, user, password string) (*ArangoStore, error) {
	endpoint := connection.NewRoundRobinEndpoints([]string{host})
	conn := connection.NewHttp2Connection(connection.DefaultHTTP2ConfigurationWrapper(endpoint, true))
	if err := conn.SetAuthentication(connection.NewBasicAuth(user, password)); err != nil {
		return nil, err
	}

	client := arangodb.NewClient(conn)

	db, err := ensureDatabase(ctx, client, dbName)
	if err != nil {
		return nil, err
	}

	col, err := ensureCollection(ctx, db)
	if err != nil {
		return nil, err
	}

	return &ArangoStore{col: col}, nil
}

func ensureDatabase(ctx context.Context, client arangodb.Client, dbName string) (arangodb.Database, error) {
	exists, err := client.DatabaseExists(ctx, dbName)
	if err != nil {
		return nil, err
	}
	if exists {
		return client.GetDatabase(ctx, dbName, nil)
	}
	return client.CreateDatabase(ctx, dbName, nil)
}

func ensureCollection(ctx context.Context, db arangodb.Database) (arangodb.Collection, error) {
	exists, err := db.CollectionExists(ctx, collectionName)
	if err != nil {
		return nil, err
	}
	if exists {
		return db.GetCollection(ctx, collectionName, nil)
	}
	return db.CreateCollectionV2(ctx, collectionName, nil)
}

func (s *ArangoStore) CreateJob(ctx context.Context, url string) (string, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	doc := map[string]interface{}{
		"_key":       id,
		"url":        url,
		"created_at": now,
		"updated_at": now,
		"status":     JobStatusCreated,
	}
	_, err := s.col.CreateDocument(ctx, doc)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *ArangoStore) SaveTier1(ctx context.Context, jobID string, r *Tier1Result) error {
	patch := map[string]interface{}{
		"tier1":      r,
		"updated_at": time.Now().UTC(),
		"status":     JobStatusTier1Complete,
	}
	_, err := s.col.UpdateDocument(ctx, jobID, patch)
	return err
}

func (s *ArangoStore) SaveTier2(ctx context.Context, jobID string, r *Tier2Result) error {
	patch := map[string]interface{}{
		"tier2":      r,
		"updated_at": time.Now().UTC(),
		"status":     JobStatusTier2Complete,
	}
	_, err := s.col.UpdateDocument(ctx, jobID, patch)
	return err
}

func (s *ArangoStore) SaveTier3(ctx context.Context, jobID string, r *Tier3Result) error {
	patch := map[string]interface{}{
		"tier3":          r,
		"updated_at":     time.Now().UTC(),
		"status":         JobStatusTier3Complete,
		"final_decision": r.Decision,
		"final_fields":   r.Fields,
	}
	_, err := s.col.UpdateDocument(ctx, jobID, patch)
	return err
}

func (s *ArangoStore) GetJob(ctx context.Context, jobID string) (*ExtractionJob, error) {
	var job ExtractionJob
	_, err := s.col.ReadDocument(ctx, jobID, &job)
	if err != nil {
		if shared.IsNotFound(err) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	return &job, nil
}
