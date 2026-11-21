package services

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// redisTimeout bounds each step of a FlushRedisDatabase call, so a Redis
// that hangs cannot hold up `routa workspace rm`.
var redisTimeout = 5 * time.Second

// FlushRedisDatabase empties one database of the Redis server at addr
// (host:port) with SELECT and FLUSHDB. It authenticates first when password
// is set, with username too when that is set. Errors never include the
// password.
func FlushRedisDatabase(addr, username, password string, db int) error {
	if db < 0 {
		return fmt.Errorf("invalid Redis database %d", db)
	}
	conn, err := net.DialTimeout("tcp", addr, redisTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	send := func(what string, args ...string) error {
		if err := conn.SetDeadline(time.Now().Add(redisTimeout)); err != nil {
			return err
		}
		if _, err := conn.Write(redisCommand(args...)); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		reply, err := readRedisReply(r)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		if strings.HasPrefix(reply, "-") {
			return fmt.Errorf("%s: %s", what, strings.TrimPrefix(reply, "-"))
		}
		return nil
	}
	if password != "" {
		args := []string{"AUTH", password}
		if username != "" {
			args = []string{"AUTH", username, password}
		}
		if err := send("AUTH", args...); err != nil {
			return err
		}
	}
	if err := send("SELECT "+strconv.Itoa(db), "SELECT", strconv.Itoa(db)); err != nil {
		return err
	}
	if err := send("FLUSHDB", "FLUSHDB"); err != nil {
		return err
	}
	_ = send("QUIT", "QUIT")
	return nil
}

// redisCommand encodes a command as a RESP array of bulk strings.
func redisCommand(args ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return []byte(b.String())
}

// readRedisReply reads one simple reply: a status (+OK), an error (-ERR …),
// or an integer (:1), returned with its type byte.
func readRedisReply(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty reply")
	}
	switch line[0] {
	case '+', '-', ':':
		return line, nil
	}
	return "", fmt.Errorf("unexpected reply %q", truncate(line, 40))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
