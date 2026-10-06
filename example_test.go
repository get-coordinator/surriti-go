package surriti_test

import (
	"context"
	"fmt"
	"log"
	"time"

	surriti "github.com/get-coordinator/surriti-go"
)

func ExampleNewSurriti() {
	cfg := surriti.DefaultDriverConfig()
	cfg.Username = "root"
	cfg.Password = "root" // Supply deployment credentials through your application.
	driver, err := surriti.NewDefaultSurrealDriver(cfg)
	if err != nil {
		log.Fatal(err)
	}

	// Nil options select deterministic dummy providers, useful for local workflows.
	memory, err := surriti.NewSurriti(driver, nil)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := memory.Close(closeCtx); err != nil {
			log.Print(err)
		}
	}()
	if _, err := memory.Connect(ctx); err != nil {
		log.Print(err)
		return
	}
	result, err := memory.AddTriplet(ctx, surriti.AddTripletRequest{
		SubjectName: "Alice", Predicate: "lives_in", ObjectName: "Philadelphia", GroupID: "example",
	})
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Println(len(result.Edges))
}

func ExampleNewDummyEmbedder() {
	embedder := surriti.NewDummyEmbedder(768)
	vectors, err := surriti.CreateBatch(context.Background(), embedder, []string{"Alice lives in Philadelphia", "Alice lives in Philadelphia"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("vectors=%d dimensions=%d similarity=%.2f\n", len(vectors), len(vectors[0]), surriti.CosineSimilarity(vectors[0], vectors[1]))
	// Output: vectors=2 dimensions=768 similarity=1.00
}
