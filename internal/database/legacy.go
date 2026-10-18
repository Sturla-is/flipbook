package database

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type viewDoc struct {
	FlipbookID string    `bson:"flipbook_id"`
	ViewedAt   time.Time `bson:"viewed_at"`
	Referrer   string    `bson:"referrer"`
	UserAgent  string    `bson:"user_agent"`
}

type settingDoc struct {
	Key   string `bson:"_id"`
	Value string `bson:"value"`
}

func (d *DB) RecordView(flipbookID, referrer, userAgent string) {
	_, _ = d.views.InsertOne(context.Background(), viewDoc{
		FlipbookID: flipbookID, ViewedAt: time.Now().UTC(), Referrer: models.NormalizeReferrer(referrer), UserAgent: userAgent,
	})
}

func (d *DB) GetViewCount(flipbookID string) int {
	count, _ := d.views.CountDocuments(context.Background(), bson.M{"flipbook_id": flipbookID})
	return int(count)
}

func FormatTime(t time.Time) string {
	return t.Format("Jan 2, 2006 3:04 PM")
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.settings.ReplaceOne(context.Background(), bson.M{"_id": key},
		settingDoc{Key: key, Value: value}, options.Replace().SetUpsert(true))
	return err
}

func (d *DB) GetSetting(key string) (string, error) {
	var doc settingDoc
	if err := d.settings.FindOne(context.Background(), bson.M{"_id": key}).Decode(&doc); err != nil {
		return "", err
	}
	return doc.Value, nil
}

// CreateSession retains the legacy raw-token representation. New authentication
// code should use CreateSessionRecord with a SHA-256 token hash instead.
func (d *DB) CreateSession(token string, ttl time.Duration) error {
	now := time.Now().UTC()
	_, err := d.sessions.InsertOne(context.Background(), bson.M{
		"_id": token, "created_at": now, "expires_at": now.Add(ttl),
	})
	return err
}

func (d *DB) ValidateSession(token string) bool {
	_, err := d.GetSessionByToken(context.Background(), token)
	return err == nil
}

func (d *DB) DeleteSession(token string) error {
	_, err := d.sessions.DeleteOne(context.Background(), bson.M{"_id": token})
	return err
}

func (d *DB) CleanExpiredSessions() {
	_, _ = d.sessions.DeleteMany(context.Background(), bson.M{"expires_at": bson.M{"$lt": time.Now().UTC()}})
}

func (d *DB) UploadToGridFS(ctx context.Context, filename string, r io.Reader) (string, error) {
	fileID, err := d.gridfs.UploadFromStream(ctx, filename, r)
	if err != nil {
		return "", fmt.Errorf("gridfs upload: %w", err)
	}
	return fileID.Hex(), nil
}

func (d *DB) DownloadFromGridFS(ctx context.Context, fileIDHex string, w io.Writer) (int64, error) {
	oid, err := bson.ObjectIDFromHex(fileIDHex)
	if err != nil {
		return 0, fmt.Errorf("invalid gridfs file id: %w", err)
	}
	return d.gridfs.DownloadToStream(ctx, oid, w)
}

func (d *DB) DeleteFromGridFS(ctx context.Context, fileIDHex string) error {
	oid, err := bson.ObjectIDFromHex(fileIDHex)
	if err != nil {
		return fmt.Errorf("invalid gridfs file id: %w", err)
	}
	return d.gridfs.Delete(ctx, oid)
}
