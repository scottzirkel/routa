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

// scanPage encodes a SCAN reply; fakeRedis adds the final CRLF.
func scanPage(cursor string, keys ...string) string {
	var b strings.Builder
	b.WriteString("*2\r\n$" + strconv.Itoa(len(cursor)) + "\r\n" + cursor + "\r\n*" + strconv.Itoa(len(keys)))
	for _, k := range keys {
		b.WriteString("\r\n$" + strconv.Itoa(len(k)) + "\r\n" + k)
	}
	return b.String()
}

func TestDeleteRedisPrefixScansEscapedAndUnlinksOnlyItsOwnKeys(t *testing.T) {
	scans := 0
	addr, got := fakeRedis(t, func(cmd []string) string {
		switch cmd[0] {
		case "SCAN":
			scans++
			if scans == 1 {
				// A key outside the prefix must never be removed, even if
				// the server returns it.
				return scanPage("17", "{a*[x]?}cache:a", "{af}cache:b")
			}
			return scanPage("0", "{a*[x]?}session:c")
		case "UNLINK":
			return ":" + strconv.Itoa(len(cmd)-1)
		}
		return ""
	})
	n, err := DeleteRedisPrefix(addr, "", "", 0, "{a*[x]?}")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	cmds := commands(got)
	want := []string{
		"SELECT 0",
		`SCAN 0 MATCH {a\*\[x\]\?}* COUNT 500`,
		"UNLINK {a*[x]?}cache:a",
		`SCAN 17 MATCH {a\*\[x\]\?}* COUNT 500`,
		"UNLINK {a*[x]?}session:c",
		"QUIT",
	}
	if strings.Join(cmds, "; ") != strings.Join(want, "; ") {
		t.Fatalf("commands = %q\nwant %q", cmds, want)
	}
}

func TestDeleteRedisPrefixBatchesAndRemovesOnlyMatchingKeys(t *testing.T) {
	t.Cleanup(func() { redisDeleteBatch = 500 })
	redisDeleteBatch = 2
	addr, got := fakeRedis(t, func(cmd []string) string {
		switch cmd[0] {
		case "SCAN":
			return scanPage("0", "p_a", "p_b", "p_c", "q_d")
		case "UNLINK":
			return "-ERR unknown command 'UNLINK'"
		case "DEL":
			return ":" + strconv.Itoa(len(cmd)-1)
		}
		return ""
	})
	n, err := DeleteRedisPrefix(addr, "", "", 3, "p_")
	if err != nil || n != 3 {
		t.Fatalf("removed %d, %v; want 3", n, err)
	}
	var removes []string
	for _, cmd := range commands(got) {
		if strings.HasPrefix(cmd, "DEL") {
			removes = append(removes, cmd)
		}
	}
	if strings.Join(removes, "; ") != "DEL p_a p_b; DEL p_c" {
		t.Fatalf("removes = %q", removes)
	}
}

func TestDeleteRedisPrefixRefusesAnEmptyPrefix(t *testing.T) {
	if _, err := DeleteRedisPrefix("127.0.0.1:1", "", "", 0, ""); err == nil || !strings.Contains(err.Error(), "empty prefix") {
		t.Fatalf("err = %v", err)
	}
}
