# Hashnode Lockfile Server

This Golang server manages lockfiles using a RESTful API and PostgreSQL for storage.

## Setup

### Prerequisites

- [Go](https://golang.org/dl/)
- [PostgreSQL](https://www.postgresql.org/download/)

### Installation

1. Clone the repository:

   ```bash
   $ git clone https://github.com/prettyirrelevant/hashnode-lockfile-server.git
   $ cd hashnode-lockfile-server
   ```

2. Set up the PostgreSQL database and configure the connection with `DATABASE_URL`.

3. Build and run the server:

   ```bash
   $ go build main.go
   $ ./main
   ```

## Configuration

- **PORT:** Server port (default: `8080`).
- **DATABASE_URL:** PostgreSQL connection string.

## Dependencies

- [Gin](https://github.com/gin-gonic/gin): Web framework.
- [sqlx](https://github.com/jmoiron/sqlx): SQL extension.
- [lib/pq](https://github.com/lib/pq): PostgreSQL driver.

## License

Feel free to reuse as you deem fit.
