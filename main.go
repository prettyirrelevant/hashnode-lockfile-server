package main

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// GithubMetaAPIResponse is the response from the Github meta API.
type GithubMetaAPIResponse struct {
	Actions []string `json:"actions"`
}

// Lockfile is the database model for the lockfile.
type Lockfile struct {
	ID             uuid.UUID            `db:"id" json:"id"`
	RepositoryName string               `db:"repository_name" json:"repositoryName"`
	RepositoryID   string               `db:"repository_id" json:"repositoryId"`
	Content        LockfileContentArray `db:"content" json:"content"`
	CreatedAt      time.Time            `db:"created_at" json:"createdAt"`
	UpdatedAt      time.Time            `db:"updated_at" json:"updatedAt"`
}

// LockfileContent is the struct for each post in the lockfile.
type LockfileContent struct {
	ID   string `json:"id" binding:"required"`
	Path string `json:"path" binding:"required"`
	Url  string `json:"url" binding:"required"`
	Hash string `json:"hash" binding:"required"`
}

// Value implements the driver.Valuer interface for LockfileContent array
func (lcArray LockfileContentArray) Value() (driver.Value, error) {
	return json.Marshal(lcArray)
}

// Scan implements the sql.Scanner interface for LockfileContent array
func (lcArray *LockfileContentArray) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, lcArray)
	case string:
		return json.Unmarshal([]byte(v), lcArray)
	default:
		return errors.New("unsupported lockfile content type")
	}
}

type LockfileContentArray []LockfileContent

type PutLockfileRequest struct {
	RepositoryName string               `json:"repositoryName" binding:"required"`
	Posts          LockfileContentArray `json:"posts" binding:"required"`
}

// TableSchema is the schema for the lockfiles table.
const TableSchema = `
CREATE TABLE IF NOT EXISTS lockfiles (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    repository_id VARCHAR(255) NOT NULL UNIQUE,
    repository_name VARCHAR(255) NOT NULL,
    content JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

var githubActionsIPs GithubMetaAPIResponse
var githubActionsIPsMutex = &sync.Mutex{}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	databaseUrl := os.Getenv("DATABASE_URL")
	if databaseUrl == "" {
		panic("DATABASE_URL is not set")
	}

	db := sqlx.MustConnect("postgres", databaseUrl)

	err := initTables(db)
	if err != nil {
		panic(err)
	}

	githubActionsIPs, err = fetchGithubActionsIPs()
	if err != nil {
		panic(err)
	}

	router := gin.Default()
	router.GET("", PingHandler)
	router.GET("/lockfiles/:repositoryId", func(ctx *gin.Context) {
		GetLockfileHandler(ctx, db)
	})
	router.PUT("/lockfiles/:repositoryId", IPFilterMiddleware(githubActionsIPs.Actions), func(ctx *gin.Context) {
		PutLockfileHandler(ctx, db)
	})

	// Fetch the Github Actions IPs every 30 minutes
	go func() {
		for {
			time.Sleep(30 * time.Minute)
			githubActionsIPsMutex.Lock()
			githubActionsIPs, err = fetchGithubActionsIPs()
			if err != nil {
				log.Println("failed to fetch github actions ips", err)
			}
			githubActionsIPsMutex.Unlock()
		}
	}()

	router.Run(fmt.Sprintf(":%s", port))
}

// PingHandler handles the GET request for the root path.
func PingHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "pong"})
}

// GetLockfileHandler handles the GET request for the lockfile.
func GetLockfileHandler(c *gin.Context, db *sqlx.DB) {
	var lockfile Lockfile

	repositoryId := c.Param("repositoryId")
	if err := db.Get(&lockfile, "SELECT * FROM lockfiles WHERE repository_id = $1", repositoryId); err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusOK, gin.H{"data": nil})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": lockfile})
}

// PutLockfileHandler handles the PUT request for the lockfile.
func PutLockfileHandler(c *gin.Context, db *sqlx.DB) {
	var request PutLockfileRequest

	repositoryId := c.Param("repositoryId")
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := db.NamedExec(
		`INSERT INTO lockfiles (repository_id, repository_name, content, updated_at)
		VALUES (:repository_id, :repository_name, :content, CURRENT_TIMESTAMP)
		ON CONFLICT (repository_id) DO UPDATE
		SET content = :content, repository_name = :repository_name, updated_at = CURRENT_TIMESTAMP`,
		map[string]interface{}{
			"repository_name": request.RepositoryName,
			"repository_id":   repositoryId,
			"content":         request.Posts,
			"updated_at":      time.Now(),
		},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": "lockfile updated successfully"})
}

// fetchGithubActionsIPs fetches the list of IPs for Github Actions.
func fetchGithubActionsIPs() (GithubMetaAPIResponse, error) {
	var decodedResp GithubMetaAPIResponse

	resp, err := http.Get("https://api.github.com/meta")
	if err != nil {
		return decodedResp, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return decodedResp, fmt.Errorf("github api error: %s", resp.Status)
	}

	if err = json.NewDecoder(resp.Body).Decode(&decodedResp); err != nil {
		return decodedResp, err
	}

	return decodedResp, nil
}

// IPFilterMiddleware checks if the request IP matches any of the IPs in the GitHub meta API response
func IPFilterMiddleware(allowedIPs []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		githubActionsIPsMutex.Lock()
		defer githubActionsIPsMutex.Unlock()
		if !isAllowedIP(c.ClientIP(), allowedIPs) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Access denied"})
			return
		}

		c.Next()
	}

}

// isAllowedIP checks if the given IP is in the list of allowed IPs or ranges
func isAllowedIP(ip string, allowedIPs []string) bool {
	clientIP := net.ParseIP(ip)
	for _, allowedIP := range allowedIPs {
		_, ipNet, _ := net.ParseCIDR(allowedIP)
		if ipNet.Contains(clientIP) {
			return true
		}
	}
	return false
}

// initTables initializes the 'lockfiles' table in the database, ensuring it exists
// and is either empty or dropped and recreated if it does not contain any rows.
func initTables(db *sqlx.DB) error {
	// Check if the 'lockfiles' table exists
	var tableExists bool
	if err := db.Get(&tableExists, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'lockfiles')`); err != nil {
		return fmt.Errorf("failed to check if table exists: %v", err)
	}

	if tableExists {
		// If the table exists, check if it is empty
		var tableEmpty bool
		if err := db.Get(&tableEmpty, `SELECT NOT EXISTS(SELECT 1 FROM lockfiles)`); err != nil {
			return fmt.Errorf("failed to check if table is empty: %v", err)
		}

		// Drop the table in two scenarios:
		// - the table is empty
		// - the table is not empty but we are not in release mode (i.e. dev mode)
		if tableEmpty || (!tableEmpty && os.Getenv("GIN_MODE") != "release") {
			if _, err := db.Exec(`DROP TABLE IF EXISTS lockfiles`); err != nil {
				return fmt.Errorf("failed to drop table: %v", err)
			}
		}
	}

	if _, err := db.Exec(TableSchema); err != nil {
		return fmt.Errorf("failed to create table: %v", err)
	}

	return nil
}
