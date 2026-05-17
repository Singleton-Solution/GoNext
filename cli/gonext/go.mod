module github.com/Singleton-Solution/GoNext/cli/gonext

go 1.24

require github.com/Singleton-Solution/GoNext/packages/go v0.0.0-00010101000000-000000000000

require (
	github.com/golang-migrate/migrate/v4 v4.19.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/lib/pq v1.10.9 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

replace github.com/Singleton-Solution/GoNext/packages/go => ../../packages/go
