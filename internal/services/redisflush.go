package services

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// redisTimeout bounds each step of a FlushRedisDatabase or DeleteRedisPrefix
// call, so a Redis that hangs cannot hold up `routa workspace rm`.
var redisTimeout = 5 * time.Second

// redisDeleteBatch is how many keys DeleteRedisPrefix asks SCAN for, and
// removes, per round trip.
var redisDeleteBatch = 500

// FlushRedisDatabase empties one database of the Redis server at addr
// (host:port) with SELECT and FLUSHDB. It authenticates first when password
// is set, with username too when that is set. Errors never include the
// password.
func FlushRedisDatabase(addr, username, password string, db int) error {
	c, err := dialRedis(addr, username, password, db)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := c.do("FLUSHDB", "FLUSHDB"); err != nil {
		return err
	}
	return nil
}

// DeleteRedisPrefix removes the keys under prefix in one database of the
// Redis server at addr, and returns how many it removed. It finds them with
// SCAN MATCH, with the pattern's glob characters escaped, and removes them in
// batches with UNLINK (DEL on a Redis too old for UNLINK). A key the server
// returns that does not start with prefix is never removed. An empty prefix
// is refused, since it would match every key. It authenticates the way
// FlushRedisDatabase does, and errors never include the password.
func DeleteRedisPrefix(addr, username, password string, db int, prefix string) (int, error) {
	if prefix == "" {
		return 0, errors.New("refusing to delete Redis keys under an empty prefix, which matches every key")
	}
	c, err := dialRedis(addr, username, password, db)
	if err != nil {
		return 0, err
	}
	defer c.close()
	pattern := RedisGlobEscape(prefix) + "*"
	count := strconv.Itoa(redisDeleteBatch)
	remove := "UNLINK"
	removed := 0
	cursor := "0"
	for {
		reply, err := c.do("SCAN", "SCAN", cursor, "MATCH", pattern, "COUNT", count)
		if err != nil {
			return removed, err
		}
		next, keys, err := scanReply(reply)
		if err != nil {
			return removed, fmt.Errorf("SCAN: %w", err)
		}
		var mine []string
		for _, k := range keys {
			if strings.HasPrefix(k, prefix) {
				mine = append(mine, k)
			}
		}
		for len(mine) > 0 {
			batch := mine[:min(len(mine), redisDeleteBatch)]
			mine = mine[len(batch):]
			reply, err := c.do(remove, append([]string{remove}, batch...)...)
			if err != nil && remove == "UNLINK" && strings.Contains(strings.ToLower(err.Error()), "unknown command") {
				remove = "DEL"
				reply, err = c.do(remove, append([]string{remove}, batch...)...)
			}
			if err != nil {
				return removed, err
			}
			if n, ok := reply.(int64); ok {
				removed += int(n)
			}
		}
		if next == "0" {
			return removed, nil
		}
		cursor = next
	}
}

// RedisGlobEscape escapes the characters Redis's MATCH patterns treat
// specially (* ? [ ] and the backslash), so a pattern matches them literally.
func RedisGlobEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// redisConn is one connection to Redis, authenticated and on one database.
type redisConn struct {
	conn net.Conn
	r    *bufio.Reader
}

// dialRedis connects to addr, authenticates when password is set, and
// selects db.
func dialRedis(addr, username, password string, db int) (*redisConn, error) {
	if db < 0 {
		return nil, fmt.Errorf("invalid Redis database %d", db)
	}
	conn, err := net.DialTimeout("tcp", addr, redisTimeout)
	if err != nil {
		return nil, err
	}
	c := &redisConn{conn: conn, r: bufio.NewReader(conn)}
	if password != "" {
		args := []string{"AUTH", password}
		if username != "" {
			args = []string{"AUTH", username, password}
		}
		if _, err := c.do("AUTH", args...); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if _, err := c.do("SELECT "+strconv.Itoa(db), "SELECT", strconv.Itoa(db)); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// do sends one command and reads its reply. what names the command in
// errors, so the password in AUTH's arguments never shows. A Redis error
// reply becomes an error.
func (c *redisConn) do(what string, args ...string) (any, error) {
	if err := c.conn.SetDeadline(time.Now().Add(redisTimeout)); err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(redisCommand(args...)); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	reply, err := readRESP(c.r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if e, ok := reply.(redisError); ok {
		return nil, fmt.Errorf("%s: %s", what, string(e))
	}
	return reply, nil
}

func (c *redisConn) close() {
	_, _ = c.do("QUIT", "QUIT")
	c.conn.Close()
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

// redisError is an error reply, without its leading "-".
type redisError string

// readRESP reads one reply: a status (+OK) as a string, an error (-ERR …) as
// a redisError, an integer (:1) as an int64, a bulk string as a string (nil
// when it is null), or an array as a []any.
func readRESP(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, errors.New("empty reply")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return redisError(line[1:]), nil
	case ':':
		return strconv.ParseInt(line[1:], 10, 64)
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("unexpected reply %q", truncate(line, 40))
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, fmt.Errorf("unexpected reply %q", truncate(line, 40))
		}
		if n < 0 {
			return nil, nil
		}
		items := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := readRESP(r)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	}
	return nil, fmt.Errorf("unexpected reply %q", truncate(line, 40))
}

// scanReply reads SCAN's reply: the next cursor and a page of keys.
func scanReply(reply any) (cursor string, keys []string, err error) {
	items, ok := reply.([]any)
	if !ok || len(items) != 2 {
		return "", nil, errors.New("unexpected reply")
	}
	cursor, ok = items[0].(string)
	if !ok {
		return "", nil, errors.New("unexpected cursor")
	}
	page, ok := items[1].([]any)
	if !ok {
		return "", nil, errors.New("unexpected key list")
	}
	for _, item := range page {
		if k, ok := item.(string); ok {
			keys = append(keys, k)
		}
	}
	return cursor, keys, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
