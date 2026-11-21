package services

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
)

// fakeRedis accepts one connection, records each command, and answers
// with the reply reply gives it, or +OK.
func fakeRedis(t *testing.T, reply func(cmd []string) string) (addr string, got chan []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan []string, 16)
	go func() {
		defer close(got)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		for {
			cmd, err := readCommand(r)
			if err != nil {
				return
			}
			got <- cmd
			out := "+OK"
			if reply != nil {
				if s := reply(cmd); s != "" {
					out = s
				}
			}
			conn.Write([]byte(out + "\r\n"))
			if cmd[0] == "QUIT" {
				return
			}
		}
	}()
	return ln.Addr().String(), got
}

func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	var args []string
	for i := 0; i < n; i++ {
		if _, err := r.ReadString('\n'); err != nil { // $len
			return nil, err
		}
		arg, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		args = append(args, strings.TrimRight(arg, "\r\n"))
	}
	return args, nil
}

func commands(got chan []string) []string {
	var out []string
	for cmd := range got {
		out = append(out, strings.Join(cmd, " "))
	}
	return out
}

func TestFlushRedisDatabaseSelectsAndFlushes(t *testing.T) {
	addr, got := fakeRedis(t, nil)
	if err := FlushRedisDatabase(addr, "", "", 37); err != nil {
		t.Fatal(err)
	}
	if cmds := strings.Join(commands(got), "; "); cmds != "SELECT 37; FLUSHDB; QUIT" {
		t.Fatalf("commands = %q", cmds)
	}
}

func TestFlushRedisDatabaseAuthenticatesFirst(t *testing.T) {
	addr, got := fakeRedis(t, nil)
	if err := FlushRedisDatabase(addr, "app", "s3cret", 5); err != nil {
		t.Fatal(err)
	}
	if cmds := commands(got); len(cmds) == 0 || cmds[0] != "AUTH app s3cret" || cmds[1] != "SELECT 5" {
		t.Fatalf("commands = %q", cmds)
	}
}

func TestFlushRedisDatabaseStopsAtAnError(t *testing.T) {
	addr, got := fakeRedis(t, func(cmd []string) string {
		if cmd[0] == "AUTH" {
			return "-WRONGPASS invalid username-password pair"
		}
		return ""
	})
	err := FlushRedisDatabase(addr, "", "s3cret", 5)
	if err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("err = %v, want the server's refusal", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("the error shows the password: %v", err)
	}
	for _, cmd := range commands(got) {
		if strings.HasPrefix(cmd, "FLUSHDB") || strings.HasPrefix(cmd, "SELECT") {
			t.Fatalf("went on after the refusal: %q", cmd)
		}
	}
}

func TestFlushRedisDatabaseWithNoServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if err := FlushRedisDatabase(addr, "", "", 5); err == nil {
		t.Fatal("no error without a server")
	}
}
