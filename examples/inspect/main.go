// Run with go run ./examples/inspect. All requests go to a local test server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/padraicbc/httpg"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var item struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(item)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := httpg.New()
	defer s.CloseIdleConnections()
	req := s.Request(http.MethodPost, server.URL).
		Context(ctx).
		Header("Accept", "application/json").
		JSON(map[string]string{"name": "coffee"}).
		Retry(false)

	fmt.Println("REQUEST")
	if _, err := req.WriteTo(os.Stdout); err != nil {
		return err
	}
	command, err := req.Curl()
	if err != nil {
		return err
	}
	fmt.Println("\nCURL\n" + command)

	res, err := req.Do()
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	fmt.Println("\nRESPONSE")
	if _, err := res.WriteTo(os.Stdout); err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", res.Status)
	}
	var item map[string]string
	if err := res.JSON(&item); err != nil {
		return err
	}
	fmt.Printf("\nDecoded after inspection: %v\n", item)
	if err := res.Body.Close(); err != nil {
		return err
	}

	replayed, err := req.Replay()
	if err != nil {
		return err
	}
	defer func() { _ = replayed.Body.Close() }()
	body, err := replayed.Text()
	if err != nil {
		return err
	}
	fmt.Printf("Replayed: %s %s", replayed.Status, body)
	fmt.Println("\nAUTOMATIC OUTPUT AND JSON DECODING")
	return s.Request(http.MethodPost, server.URL).
		JSON(map[string]string{"name": "tea"}).
		Output(os.Stdout).
		DoJSON(&item)
}
