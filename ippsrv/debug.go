package ippsrv

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"

	"github.com/OpenPrinting/goipp"
)

func (s *Server) DebugServer(ctx context.Context, addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go serve(conn)
	}
}

func serve(conn net.Conn) error {
	defer conn.Close()
	if _, err := io.Copy(os.Stdout, conn); err != nil {
		return err
	}
	return nil
}

func dumpfile(filename string, a any) {
	f, err := os.Create(filename)
	if err != nil {
		slog.Error("dumpfile", "err", err, "filename", filename)
		return
	}
	defer f.Close()
	dump(f, a)
}

func dumpIPPFile(filename string, msg *goipp.Message) {
	f, err := os.Create(filename)
	if err != nil {
		slog.Error("dumpIPPFile", "err", err, "filename", filename)
		return
	}
	defer f.Close()
	dumpIPP(f, msg)
}

func dumpIPP(w io.Writer, msg *goipp.Message) {
	fm := goipp.NewFormatter()
	fm.FmtRequest(msg)
	if _, err := fm.WriteTo(w); err != nil {
		slog.Error("dumpIPP", "err", err)
		return
	}
}

func dump(w io.Writer, a any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		slog.Error("dump", "err", err)
		return
	}
}
