package services

// standardPorts are the ports each service listens on out of the box. routa
// runs its own services on other ports (Redis on 6380, databases on 3307 and
// up, and so on), so a server on one of these is a system service or someone
// else's. routa reads from such servers but never changes their data: it does
// not copy, create, or drop databases there, or hand out their Redis numbers.
var standardPorts = map[string]string{
	MySQLDefaultPort:        "MySQL, MariaDB, and SingleStore",
	PostgresDefaultPort:     "Postgres",
	RedisDefaultPort:        "Redis",
	MeilisearchDefaultPort:  "Meilisearch",
	TypesenseDefaultPort:    "Typesense",
	MinIODefaultPort:        "MinIO",
	MinIODefaultConsolePort: "MinIO's console",
	MailpitSMTPPort:         "Mailpit's SMTP",
	MailpitWebPort:          "Mailpit",
}

// StandardPortOwner names the services whose standard port this is, or ""
// when it is not a standard port.
func StandardPortOwner(port string) string {
	return standardPorts[port]
}
