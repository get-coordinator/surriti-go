package surrealtransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestServerCloseUnblocksIdleAndPendingQueries(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "idle"
		if pending {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			signal := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if pending {
					if _, _, err = conn.ReadMessage(); err != nil {
						return
					}
				} else {
					<-signal
				}
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "restart"), time.Now().Add(time.Second))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := Open(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close(ctx)
			if !pending {
				close(signal)
				select {
				case <-client.lifetime.Done():
				case <-ctx.Done():
					t.Fatal("peer close did not mark connection unavailable")
				}
			}
			_, err = client.Query(ctx, "RETURN 1;", nil)
			if err == nil || !strings.Contains(err.Error(), "websocket connection closed") {
				t.Fatalf("expected reconnectable close error, got %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("query waited for caller timeout instead of peer close")
			}
		})
	}
}

func TestAbruptDisconnectUnblocksQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Read a request and drop TCP without a WebSocket close frame.
		_, _, _ = conn.ReadMessage()
		_ = conn.Close()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := Open(ctx, "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(ctx)
	_, err = client.Query(ctx, "RETURN 1;", nil)
	if err == nil || !strings.Contains(err.Error(), "websocket connection closed") {
		t.Fatalf("expected reconnectable disconnect error, got %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("request stalled until caller deadline")
	}
}
