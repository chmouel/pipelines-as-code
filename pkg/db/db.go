package db

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Queue struct {
	ID               int       `gorm:"primaryKey"`
	Name             string    `gorm:"name"`
	CreatedAt        time.Time `gorm:"created_at"`
	UpdatedAt        time.Time `gorm:"updated_at"`
	Repository       string    `gorm:"repository"`
	GitHubCheckRunID int64     `gorm:"gh_check_run_id"`
	GitHubStatusURL  string    `gorm:"gh_status_url"`
	State            string    `gorm:"state"`
}

type DB struct {
	Cnx    *gorm.DB
	logger *zap.SugaredLogger
}

func NewDB(logger *zap.SugaredLogger) *DB {
	return &DB{logger: logger}
}

func (db *DB) addPipelineRun(pr *tektonv1.PipelineRun, q *Queue) error {
	if q == nil {
		q = &Queue{}
	}
	if db.Cnx == nil {
		return nil
	}
	q.CreatedAt = time.Now()
	if q.Name == "" {
		q.Name = pr.GetName()
	}
	if q.Repository == "" {
		q.Repository = pr.GetAnnotations()[keys.Repository]
	}
	result := db.Cnx.Create(q)
	return result.Error
}

func (db *DB) CreatedUpdatePR(pr *tektonv1.PipelineRun, q *Queue) error {
	if q == nil {
		q = &Queue{}
	}
	if db.Cnx == nil {
		return nil
	}
	q.UpdatedAt = time.Now()
	name := pr.GetName()
	if name == "" {
		name = pr.GetGenerateName()
	}
	update := db.Cnx.Model(q).Where("name = ?", name).Updates(q)
	if update.RowsAffected == 0 {
		return db.addPipelineRun(pr, q)
	}
	return update.Error
}

func (db *DB) Connect() error {
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
	db.logger.Infof("Connected to database %s", databaseType)
	if err := dbc.AutoMigrate(&Queue{}); err != nil {
		return err
	}
	db.Cnx = dbc
	return nil
}
