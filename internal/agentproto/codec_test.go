package agentproto

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
)

type ping struct{ N int }

func TestBidiJSONOverH2(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/x.v1.S/Bidi", connect.NewBidiStreamHandler("/x.v1.S/Bidi", func(ctx context.Context, s *connect.BidiStream[ping, ping]) error {
		for {
			m, err := s.Receive()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := s.Send(&ping{N: m.N * 2}); err != nil {
				return err
			}
		}
	}, connect.WithCodec(Codec{})))
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	c := connect.NewClient[ping, ping](srv.Client(), srv.URL+"/x.v1.S/Bidi", connect.WithCodec(Codec{}))
	st := c.CallBidiStream(context.Background())
	for i := 1; i <= 3; i++ {
		if err := st.Send(&ping{N: i}); err != nil {
			t.Fatal(err)
		}
		r, err := st.Receive()
		if err != nil || r.N != i*2 {
			t.Fatalf("recv %v %v", r, err)
		}
	}
	st.CloseRequest()
	st.CloseResponse()
}
