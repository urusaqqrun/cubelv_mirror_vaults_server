package database

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/urusaqqrun/vault-mirror-service/model"
)

// upsertDoc 通用 upsert 邏輯：從 doc 取出 _id 作為 filter，$set 時移除 _id 避免 MongoDB 錯誤
func (m *MongoReader) upsertDoc(ctx context.Context, col *mongo.Collection, userID string, doc bson.M) error {
	id, ok := doc["_id"].(string)
	if !ok || id == "" {
		return fmt.Errorf("missing _id in doc")
	}
	// $set 不可包含 _id，否則 MongoDB 會報錯或靜默忽略
	setDoc := make(bson.M, len(doc))
	for k, v := range doc {
		if k == "_id" {
			continue
		}
		setDoc[k] = v
	}
	setDoc["memberID"] = userID
	_, err := col.UpdateOne(ctx,
		bson.M{"_id": id, "memberID": userID},
		bson.M{"$set": setDoc},
		options.Update().SetUpsert(true),
	)
	return err
}

// UpsertFolder 以 _id 為 key 做 upsert
func (m *MongoReader) UpsertFolder(ctx context.Context, userID string, doc bson.M) error {
	return m.upsertDoc(ctx, m.foldersCol(), userID, doc)
}

// UpsertNote 以 _id 為 key 做 upsert
func (m *MongoReader) UpsertNote(ctx context.Context, userID string, doc bson.M) error {
	return m.upsertDoc(ctx, m.notesCol(), userID, doc)
}

// UpsertCard 以 _id 為 key 做 upsert
func (m *MongoReader) UpsertCard(ctx context.Context, userID string, doc bson.M) error {
	return m.upsertDoc(ctx, m.cardsCol(), userID, doc)
}

// UpsertChart 以 _id 為 key 做 upsert
func (m *MongoReader) UpsertChart(ctx context.Context, userID string, doc bson.M) error {
	return m.upsertDoc(ctx, m.chartsCol(), userID, doc)
}

// UpsertItem 以 _id 為 key 對 Item collection 做 upsert（fields.memberID 作為 filter）
func (m *MongoReader) UpsertItem(ctx context.Context, userID string, doc bson.M) error {
	id, ok := doc["_id"].(string)
	if !ok || id == "" {
		return fmt.Errorf("missing _id in item doc")
	}
	setDoc := make(bson.M, len(doc))
	for k, v := range doc {
		if k == "_id" {
			continue
		}
		setDoc[k] = v
	}
	// 確保 fields.memberID 正確，並與既有文件比對缺失欄位做 $unset
	var fields bson.M
	switch typed := setDoc["fields"].(type) {
	case bson.M:
		fields = typed
	case map[string]interface{}:
		fields = bson.M(typed)
	default:
		fields = bson.M{}
		setDoc["fields"] = fields
	}
	fields["memberID"] = userID

	unsetDoc := bson.M{}
	var existing struct {
		Fields bson.M `bson:"fields"`
	}
	err := m.itemsCol().FindOne(ctx, bson.M{"_id": id, "fields.memberID": userID},
		options.FindOne().SetProjection(bson.M{"fields": 1}),
	).Decode(&existing)
	if err != nil && err != mongo.ErrNoDocuments {
		return err
	}
	for k := range existing.Fields {
		if k == "memberID" {
			continue
		}
		if _, exists := fields[k]; !exists {
			unsetDoc["fields."+k] = ""
		}
	}

	update := bson.M{"$set": setDoc}
	if len(unsetDoc) > 0 {
		update["$unset"] = unsetDoc
	}
	_, err = m.itemsCol().UpdateOne(ctx, bson.M{"_id": id, "fields.memberID": userID}, update, options.Update().SetUpsert(true))
	return err
}

// DeleteItemDoc 從 Item collection 刪除文件並寫入 ItemDeletionLog
func (m *MongoReader) DeleteItemDoc(ctx context.Context, userID, docID string, usn int) error {
	res, err := m.itemsCol().DeleteOne(ctx, bson.M{"_id": docID, "fields.memberID": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount > 0 {
		if logErr := m.writeDeletionLog(ctx, userID, "item", docID, usn); logErr != nil {
			log.Printf("[DeleteItemDoc] deletion log write error (item/%s): %v", docID, logErr)
		}
	}
	return nil
}

// DeleteDocument 刪除指定 collection 的文件，並寫入對應的 Deletion Log。
func (m *MongoReader) DeleteDocument(ctx context.Context, userID, collection, docID string, usn int) error {
	if collection == "item" {
		return m.DeleteItemDoc(ctx, userID, docID, usn)
	}
	var col *mongo.Collection
	switch collection {
	case "note":
		col = m.notesCol()
	case "card":
		col = m.cardsCol()
	case "chart":
		col = m.chartsCol()
	default:
		col = m.foldersCol()
	}
	res, err := col.DeleteOne(ctx, bson.M{"_id": docID, "memberID": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount > 0 {
		if logErr := m.writeDeletionLog(ctx, userID, collection, docID, usn); logErr != nil {
			log.Printf("[DeleteDocument] deletion log write error (%s/%s): %v", collection, docID, logErr)
		}
	}
	return nil
}

// writeDeletionLog 寫入 deletion log，欄位格式與 noteceo_golang_service 一致
func (m *MongoReader) writeDeletionLog(ctx context.Context, userID, collection, docID string, usn int) error {
	now := time.Now().UnixMilli()
	switch collection {
	case "note":
		_, err := m.db.Collection("NoteDeletionLog").InsertOne(ctx, bson.M{
			"noteID":   docID,
			"memberID": userID,
			"deleteAt": now,
			"usn":      usn,
		})
		return err
	case "folder":
		_, err := m.db.Collection("FolderDeletionLog").InsertOne(ctx, bson.M{
			"folderID": docID,
			"memberID": userID,
			"deleteAt": now,
			"usn":      usn,
		})
		return err
	case "card":
		nowStr := strconv.FormatInt(now, 10)
		_, err := m.db.Collection("DeletedCard").InsertOne(ctx, bson.M{
			"deletedID": docID,
			"memberID":  userID,
			"deletedAt": nowStr,
			"usn":       usn,
		})
		return err
	case "chart":
		nowStr := strconv.FormatInt(now, 10)
		_, err := m.db.Collection("DeletedChart").InsertOne(ctx, bson.M{
			"deletedID": docID,
			"memberID":  userID,
			"deletedAt": nowStr,
			"usn":       usn,
		})
		return err
	case "item":
		_, err := m.db.Collection("ItemDeletionLog").InsertOne(ctx, bson.M{
			"itemID":   docID,
			"memberID": userID,
			"deleteAt": now,
			"usn":      usn,
		})
		return err
	default:
		return fmt.Errorf("unknown collection for deletion log: %s", collection)
	}
}

// IncrementUSN 遞增用戶 USN，使用 Redis INCR 搭配 MongoDB fallback。
// Redis key 格式 usn:{memberID} 與 noteceo_golang_service 共用。
func (m *MongoReader) IncrementUSN(ctx context.Context, userID string) (int, error) {
	if m.rdb != nil {
		return m.incrementUSNViaRedis(ctx, userID)
	}
	return m.incrementUSNViaMongo(ctx, userID)
}

func (m *MongoReader) incrementUSNViaRedis(ctx context.Context, userID string) (int, error) {
	key := "usn:" + userID

	// 只在 key 不存在時做一次 SetNX 初始化，避免 EXISTS -> SetNX 的 TOCTOU。
	if _, err := m.rdb.Get(ctx, key).Result(); err == redis.Nil {
		baseUSN, baseErr := m.getUserUSNFromMongo(ctx, userID)
		if baseErr != nil {
			baseUSN = 0
		}
		if _, setErr := m.rdb.SetNX(ctx, key, baseUSN, 0).Result(); setErr != nil {
			log.Printf("Redis SetNX 初始化 USN 失敗（忽略，繼續 INCR）: %v", setErr)
		}
	} else if err != nil {
		log.Printf("Redis GET USN 失敗（忽略，繼續 INCR）: %v", err)
	}

	newUSN, err := m.rdb.Incr(ctx, key).Result()
	if err != nil {
		log.Printf("Redis INCR 失敗，fallback MongoDB: %v", err)
		return m.incrementUSNViaMongo(ctx, userID)
	}

	// 標記待同步（由 golang_service 的 SyncWorker 寫回 MongoDB）
	m.rdb.SAdd(ctx, "usn:dirty", userID)

	return int(newUSN), nil
}

func (m *MongoReader) getUserUSNFromMongo(ctx context.Context, userID string) (int, error) {
	coll := m.db.Collection("User")
	var row struct {
		Usn int `bson:"usn"`
	}
	err := coll.FindOne(ctx, buildUserFilter(userID),
		options.FindOne().SetProjection(bson.M{"usn": 1}),
	).Decode(&row)
	if err != nil {
		return 0, err
	}
	return row.Usn, nil
}

func (m *MongoReader) incrementUSNViaMongo(ctx context.Context, userID string) (int, error) {
	coll := m.db.Collection("User")
	after := options.After
	res := coll.FindOneAndUpdate(ctx,
		buildUserFilter(userID),
		bson.M{"$inc": bson.M{"usn": 1}},
		&options.FindOneAndUpdateOptions{ReturnDocument: &after},
	)
	var row struct {
		Usn int `bson:"usn"`
	}
	if err := res.Decode(&row); err != nil {
		return 0, err
	}
	return row.Usn, nil
}

func buildUserFilter(userID string) bson.M {
	orFilters := bson.A{
		bson.M{"_id": userID},
		bson.M{"memberID": userID},
	}
	if oid, err := primitive.ObjectIDFromHex(userID); err == nil {
		orFilters = append(orFilters, bson.M{"_id": oid})
	}
	return bson.M{"$or": orFilters}
}

// ListAllNotes 回傳用戶所有 Note（全量匯出用）
func (m *MongoReader) ListAllNotes(ctx context.Context, userID string) ([]*model.Note, error) {
	cur, err := m.notesCol().Find(ctx, bson.M{"memberID": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []*model.Note
	for cur.Next(ctx) {
		var n model.Note
		if err := cur.Decode(&n); err != nil {
			log.Printf("[ListAllNotes] decode error: %v", err)
			continue
		}
		out = append(out, &n)
	}
	return out, cur.Err()
}

// ListAllCards 回傳用戶所有 Card（全量匯出用）
func (m *MongoReader) ListAllCards(ctx context.Context, userID string) ([]*model.Card, error) {
	cur, err := m.cardsCol().Find(ctx, bson.M{"memberID": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []*model.Card
	for cur.Next(ctx) {
		var c model.Card
		if err := cur.Decode(&c); err != nil {
			log.Printf("[ListAllCards] decode error: %v", err)
			continue
		}
		out = append(out, &c)
	}
	return out, cur.Err()
}

// ListAllCharts 回傳用戶所有 Chart（全量匯出用）
func (m *MongoReader) ListAllCharts(ctx context.Context, userID string) ([]*model.Chart, error) {
	cur, err := m.chartsCol().Find(ctx, bson.M{"memberID": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []*model.Chart
	for cur.Next(ctx) {
		var c model.Chart
		if err := cur.Decode(&c); err != nil {
			log.Printf("[ListAllCharts] decode error: %v", err)
			continue
		}
		out = append(out, &c)
	}
	return out, cur.Err()
}
