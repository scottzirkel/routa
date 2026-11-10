package workspace

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/scottzirkel/routa/internal/services"
)

// cloneMySQL copies database from into to on the same MySQL or MariaDB
// server. The data never leaves the server: tables come from a schema-only
// dump, rows from one INSERT … SELECT per table, and triggers last so they do
// not fire on copied rows. Generated columns are left out of the INSERT, since
// MySQL computes them and rejects given values. Row counts are compared at
// the end; a mismatch is only a warning, because the parent may be in use.
func cloneMySQL(from, to string, tools services.DatabaseTools, defaults string, out io.Writer) error {
	schema := exec.Command(tools.Dump, defaults, "--no-data", "--skip-triggers", "--routines", "--events", "--no-tablespaces", "--single-transaction", from)
	if err := pipe(schema, exec.Command(tools.Client, defaults, to)); err != nil {
		return fmt.Errorf("copy schema: %w", err)
	}

	columns, err := mysqlQuery(tools, defaults, fmt.Sprintf(`SELECT c.TABLE_NAME, c.COLUMN_NAME
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = %s AND t.TABLE_TYPE = 'BASE TABLE'
  AND c.EXTRA NOT LIKE '%%VIRTUAL GENERATED%%'
  AND c.EXTRA NOT LIKE '%%STORED GENERATED%%'
  AND c.EXTRA NOT LIKE '%%PERSISTENT GENERATED%%'
ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`, sqlString(from)))
	if err != nil {
		return fmt.Errorf("read columns: %w", err)
	}
	tables := tableColumns(columns)
	if err := runMySQLScript(tools, defaults, copyRowsScript(from, to, tables)); err != nil {
		return fmt.Errorf("copy rows: %w", err)
	}

	triggers := exec.Command(tools.Dump, defaults, "--no-data", "--no-create-info", "--triggers", "--no-tablespaces", from)
	if err := pipe(triggers, exec.Command(tools.Client, defaults, to)); err != nil {
		return fmt.Errorf("copy triggers: %w", err)
	}

	if len(tables) == 0 {
		return nil
	}
	counts, err := mysqlQuery(tools, defaults, rowCountQuery(from, to, tables))
	if err != nil {
		return fmt.Errorf("compare row counts: %w", err)
	}
	if diff := countMismatches(counts); len(diff) > 0 {
		fmt.Fprintf(out, "  warning: row counts differ for %s; the parent database may have changed while copying\n", strings.Join(diff, ", "))
	} else {
		fmt.Fprintf(out, "  copied %d tables; row counts match\n", len(tables))
	}
	return nil
}

type tableCols struct {
	name    string
	columns []string
}

// tableColumns groups "table<TAB>column" rows, keeping their order.
func tableColumns(rows [][]string) []tableCols {
	var out []tableCols
	for _, row := range rows {
		if len(row) != 2 {
			continue
		}
		if n := len(out); n == 0 || out[n-1].name != row[0] {
			out = append(out, tableCols{name: row[0]})
		}
		out[len(out)-1].columns = append(out[len(out)-1].columns, row[1])
	}
	return out
}

// copyRowsScript is one session that copies every table's rows. Foreign key
// and unique checks are off so tables can load in any order, and
// NO_AUTO_VALUE_ON_ZERO keeps rows whose id is 0.
func copyRowsScript(from, to string, tables []tableCols) string {
	var b strings.Builder
	b.WriteString("SET SESSION foreign_key_checks = 0;\nSET SESSION unique_checks = 0;\nSET SESSION sql_mode = 'NO_AUTO_VALUE_ON_ZERO';\n")
	for _, t := range tables {
		cols := make([]string, len(t.columns))
		for i, c := range t.columns {
			cols[i] = mysqlIdent(c)
		}
		list := strings.Join(cols, ", ")
		fmt.Fprintf(&b, "INSERT INTO %s.%s (%s) SELECT %s FROM %s.%s;\n",
			mysqlIdent(to), mysqlIdent(t.name), list, list, mysqlIdent(from), mysqlIdent(t.name))
	}
	return b.String()
}

func rowCountQuery(from, to string, tables []tableCols) string {
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf("SELECT %s, (SELECT COUNT(*) FROM %s.%s), (SELECT COUNT(*) FROM %s.%s)",
			sqlString(t.name), mysqlIdent(from), mysqlIdent(t.name), mysqlIdent(to), mysqlIdent(t.name))
	}
	return strings.Join(parts, " UNION ALL ")
}

func countMismatches(rows [][]string) []string {
	var diff []string
	for _, row := range rows {
		if len(row) == 3 && row[1] != row[2] {
			a, _ := strconv.Atoi(row[1])
			b, _ := strconv.Atoi(row[2])
			diff = append(diff, fmt.Sprintf("%s (%d → %d)", row[0], a, b))
		}
	}
	return diff
}

// mysqlQuery runs one query and returns its rows, split on tabs. --raw keeps
// names exactly as stored.
func mysqlQuery(tools services.DatabaseTools, defaults, query string) ([][]string, error) {
	cmd := exec.Command(tools.Client, defaults, "--batch", "--raw", "--skip-column-names", "-e", query)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	outBytes, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(string(outBytes), "\n"), "\n") {
		if line != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	return rows, nil
}

func runMySQLScript(tools services.DatabaseTools, defaults, script string) error {
	cmd := exec.Command(tools.Client, defaults)
	cmd.Stdin = strings.NewReader(script)
	return run(cmd)
}

func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s) + "'"
}
