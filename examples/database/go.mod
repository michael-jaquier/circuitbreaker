module github.com/michael-jaquier/circuitbreaker/examples/database

go 1.25.5

require (
	github.com/michael-jaquier/circuitbreaker v0.0.0
	github.com/mattn/go-sqlite3 v1.14.18
)

replace github.com/michael-jaquier/circuitbreaker => ../..
