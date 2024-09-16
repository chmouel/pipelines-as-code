package db

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Queue struct {
	ID               int       `gorm:"primaryKey"`
	Name             string    `gorm:"name"`
	CreatedAt        time.Time `gorm:"created_at"`
	Repository       string    `gorm:"repository"`
	GitHubCheckRunId int64     `gorm:"gh_check_run_id"`
	GitHubStatusURL  string    `gorm:"gh_status_url"`
}

type DB struct {
	Cnx     *gorm.DB
	clients clients.Clients
}

func NewDB(c clients.Clients) *DB {
	return &DB{clients: c}
}

func (db *DB) AddPipelineRun(pr *tektonv1.PipelineRun) error {
	if db.Cnx == nil {
		return nil
	}
	result := db.Cnx.Create(&Queue{
		Name:       pr.GetName(),
		CreatedAt:  time.Now(),
		Repository: pr.GetAnnotations()[keys.Repository],
	})
	return result.Error
}

func (db *DB) Connect(ctx context.Context) error {
	var dbc *gorm.DB
	var err error
	var databaseType string
	if os.Getenv("POSTGRESQL_URI") != "" {
		dbc, err = gorm.Open(postgres.Open(os.Getenv("POSTGRESQL_URI")), &gorm.Config{})
		re := regexp.MustCompile(`password=[^&]*`)
		envWithoutPassword := re.ReplaceAllString(os.Getenv("POSTGRESQL_URI"), "password=****")
		databaseType = fmt.Sprintf("PostgreSQL: %s", envWithoutPassword)
	} else {
		// TODO: logger should be used here
		return nil
	}

	if err != nil {
		return err
	}
	db.clients.Log.Infof("Connected to database %s", databaseType)

	if err := dbc.AutoMigrate(&Queue{}); err != nil {
		return err
	}
	db.Cnx = dbc
	return nil
}
