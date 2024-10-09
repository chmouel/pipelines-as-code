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
	ID             int       `gorm:"primaryKey"`
	Name           string    `gorm:"name"`
	CreatedAt      time.Time `gorm:"created_at"`
	UpdatedAt      time.Time `gorm:"updated_at"`
	RepositoryName string    `gorm:"repository"`
	Namespace      string    `gorm:"namespace"`
	OriginalPRName string    `gorm:"original_pr_name"`
	State          string    `gorm:"state"`
}

type DB struct {
	Cnx    *gorm.DB
	logger *zap.SugaredLogger
}

func NewDB(logger *zap.SugaredLogger) *DB {
	return &DB{logger: logger}
}

func (db *DB) createPipelineRun(pr *tektonv1.PipelineRun, q *Queue) error {
	if db.Cnx == nil {
		return nil
	}
	if q == nil {
		q = &Queue{State: keys.StateQueued}
	}
	q.CreatedAt = time.Now()
	if q.Name == "" {
		q.Name = pr.GetName()
	}
	if q.RepositoryName == "" {
		q.RepositoryName = pr.GetAnnotations()[keys.Repository]
	}
	if q.Namespace == "" {
		q.Namespace = pr.GetNamespace()
	}

	sql := db.Cnx.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Create(q)
	})
	db.logger.Debugf("SQL: %s", sql)
	result := db.Cnx.Create(q)
	return result.Error
}

func (db *DB) AddUpdatePR(pr *tektonv1.PipelineRun, q *Queue) error {
	if db.Cnx == nil {
		return nil
	}
	if q == nil {
		q = &Queue{State: keys.StateQueued}
	}
	q.UpdatedAt = time.Now()
	update := db.Cnx.Model(q).Where("name = ?", pr.GetName()).Updates(q)
	if update.RowsAffected == 0 {
		return db.createPipelineRun(pr, q)
	}
	return update.Error
}

func (db *DB) GetQueuedPRs(limit int, namespace, repo string) ([]Queue, error) {
	if db.Cnx == nil {
		return nil, nil
	}
	var queues []Queue
	query := db.Cnx.Where(
		Queue{Namespace: namespace, RepositoryName: repo, State: keys.StateQueued},
	)
	if limit > 0 {
		query = query.Limit(limit)
	}
	query = query.Order("created_at asc")

	result := query.Find(&queues)
	return queues, result.Error
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
