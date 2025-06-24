package main

import (
	"context"
	"log"

	"github.com/qdrant/go-client/qdrant"
)

var qdrantClient *qdrant.Client

func initQdrant() {
	cfg := &qdrant.Config{
		Host: "localhost",
		Port: 6334, // gRPC порт
	}

	client, err := qdrant.NewClient(cfg)
	if err != nil {
		log.Fatalf("Ошибка при создании Qdrant-клиента: %v", err)
	}
	qdrantClient = client

	// Создаём (или обновляем) коллекцию
	createReq := &qdrant.CreateCollection{
		CollectionName: "my_collection",
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     384,
			Distance: qdrant.Distance_Cosine,
		}),
	}

	err = qdrantClient.CreateCollection(context.Background(), createReq)
	if err != nil {
		log.Printf("Ошибка при создании/обновлении коллекции (возможно, уже существует): %v", err)
	} else {
		log.Println("Коллекция my_collection создана/обновлена!")
	}
}

// computeEmbedding - пока заглушка (384 нулей)
func computeEmbedding(text string) []float32 {
	vec := make([]float32, 384)
	// TODO: подключить реальную модель эмбеддингов
	return vec
}

// indexChunks - заливаем чанки в Qdrant
func indexChunks(chunks []string) error {
	var points []*qdrant.PointStruct
	for i, c := range chunks {
		embedding := computeEmbedding(c)
		pt := &qdrant.PointStruct{
			Id:      qdrant.NewIDNum(uint64(i + 1)),
			Vectors: qdrant.NewVectors(embedding...),
			Payload: qdrant.NewValueMap(map[string]any{
				"chunk_text": c,
			}),
		}
		points = append(points, pt)
	}

	upsertReq := &qdrant.UpsertPoints{
		CollectionName: "my_collection",
		Points:         points,
	}

	_, err := qdrantClient.Upsert(context.Background(), upsertReq)
	return err
}

// searchInQdrant - ищем top-N релевантных
func searchInQdrant(vec []float32, limit int) ([]*qdrant.ScoredPoint, error) {
	limitU64 := uint64(limit)
	req := &qdrant.QueryPoints{
		CollectionName: "my_collection",
		Query:          qdrant.NewQuery(vec...),
		Limit:          &limitU64,
		WithPayload:    qdrant.NewWithPayload(true),
	}

	res, err := qdrantClient.Query(context.Background(), req)
	if err != nil {
		return nil, err
	}

	// возвращает []*qdrant.ScoredPoint
	return res, nil
}
