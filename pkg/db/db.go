package db

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/google/go-github/v64/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Queue struct {
	ID               int       `gorm:"primaryKey"`
	Name             string    `gorm:"name"`
	Namespace        string    `gorm:"namespace"`
	CreatedAt        time.Time `gorm:"created_at"`
	UpdatedAt        time.Time `gorm:"updated_at"`
	Repository       string    `gorm:"repository"`
	GitHubCheckRunID int64     `gorm:"gh_check_run_id"`
	GitHubStatusURL  string    `gorm:"gh_status_url"`
	State            string    `gorm:"state"`
	Queued           *bool     `gorm:"queued"`
	OriginalPRName   string    `gorm:"original_pr_name"`
}

type DB struct {
	Cnx    *gorm.DB
	logger *zap.SugaredLogger
}

func NewDB(logger *zap.SugaredLogger) *DB {
	return &DB{logger: logger}
}

func (db *DB) createPipelineRun(pr *tektonv1.PipelineRun, q *Queue) error {
	if q == nil {
		q = &Queue{
			Queued: github.Bool(true),
		}
	}
	if db.Cnx == nil {
		return nil
	}
	q.CreatedAt = time.Now()
	if q.Name == "" {
		q.Name = pr.GetName()
	}
	if q.OriginalPRName == "" {
		q.OriginalPRName = pr.GetAnnotations()[keys.OriginalPRName]
	}
	if q.Repository == "" {
		q.Repository = pr.GetAnnotations()[keys.Repository]
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

func (db *DB) RemoveRepository(name, namespace string) error {
	if db.Cnx == nil {
		return nil
	}
	sql := db.Cnx.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Where("repository = ? AND namespace = ?", name, namespace).Delete(&Queue{})
	})
	db.logger.Debugf("SQL: %s", sql)
	result := db.Cnx.Where("repository = ? AND namespace = ?", name, namespace).Delete(&Queue{})
	return result.Error
}

func (db *DB) RemovePipelineRun(repo *v1alpha1.Repository, prName string) (string, error) {
	if db.Cnx == nil {
		return "", nil
	}

	sql := db.Cnx.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Where("name = ? AND repository = ?", prName, repo.GetName()).Delete(&Queue{})
	})
	db.logger.Infof("SQL: %s", sql)

	result := db.Cnx.Where("name = ? AND repository = ?", prName, repo.GetName()).Delete(&Queue{})
	if result.Error != nil {
		return "", result.Error
	}

	return db.GetNextInQueue(repo)
}

func (db *DB) GetNextInQueue(repo *v1alpha1.Repository) (string, error) {
	if db.Cnx == nil {
		return "", nil
	}
	var q Queue
	result := db.Cnx.Where("repository = ? and queued = ?", repo.GetName(), true).Order("SUBSTRING(name FROM '([0-9]+)')::BIGINT ASC, name").First(&q)
	if result.Error != nil {
		return "", result.Error
	}
	return fmt.Sprintf("%s/%s", q.Repository, q.Name), nil
}

func (db *DB) CreatedUpdatePR(pr *tektonv1.PipelineRun, q *Queue) error {
	if q == nil {
		q = &Queue{Queued: github.Bool(true)}
	}
	if db.Cnx == nil {
		return nil
	}
	q.UpdatedAt = time.Now()
	sql := db.Cnx.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Model(q).Where("name = ?", pr.GetName()).Updates(q)
	})
	db.logger.Debugf("SQL: %s", sql)
	update := db.Cnx.Model(q).Where("name = ?", pr.GetName()).Updates(q)
	if update.RowsAffected == 0 {
		return db.createPipelineRun(pr, q)
	}
	return update.Error
}

func (db *DB) GetPipelineRunToCleanup(pr *tektonv1.PipelineRun, maxKeepRun int) ([]Queue, error) {
	var queues []Queue
	var totalRows int64
	if db.Cnx == nil {
		return nil, nil
	}
	original_pr_name := pr.GetAnnotations()[keys.OriginalPRName]
	repository := pr.GetAnnotations()[keys.Repository]
	db.Cnx.Model(&Queue{}).Where("original_pr_name = ? AND repository = ? AND queued = ?", original_pr_name, repository, false).Count(&totalRows)
	if totalRows <= int64(maxKeepRun) {
		return nil, nil
	}
	db.Cnx.Where("original_pr_name = ? AND repository = ? AND queued = ?", original_pr_name, repository, false).Order("created_at asc").Limit(int(totalRows - int64(maxKeepRun))).Find(&queues)
	return queues, db.Cnx.Error
}

func (db *DB) GetQueue(pr *tektonv1.PipelineRun) ([]string, error) {
	if db.Cnx == nil {
		return nil, nil
	}
	queues := []Queue{}
	repository := pr.GetAnnotations()[keys.Repository]
	tx := db.Cnx.Where(&Queue{Repository: repository, Queued: github.Bool(true)}).Order("SUBSTRING(name FROM '([0-9]+)')::BIGINT ASC, name").Find(&queues)
	runningQ := []Queue{}
	tx = db.Cnx.Find(&queues).Where(&Queue{Repository: repository, State: "started"}).Order("SUBSTRING(name FROM '([0-9]+)')::BIGINT ASC, name").Find(&runningQ)

	mergedQueues := make([]Queue, len(queues)+len(runningQ))
	copy(mergedQueues, runningQ)
	copy(mergedQueues[len(runningQ):], queues)
	ret := make([]string, len(mergedQueues))
	for i, q := range mergedQueues {
		ret[i] = fmt.Sprintf("%s/%s", q.Repository, q.Name)
	}
	return ret, tx.Error
}

func (db *DB) Connect() error {
	var dbc *gorm.DB
	var err error
	var databaseType string
	loggerConfig := logger.Config{
		SlowThreshold:             time.Second, // Slow SQL threshold
		IgnoreRecordNotFoundError: true,        // Ignore ErrRecordNotFound error for logger
		ParameterizedQueries:      true,        // Don't include params in the SQL log
		Colorful:                  true,        // Disable color
	}
	if _, exist := os.LookupEnv("DATABASE_DEBUG"); exist {
		loggerConfig.LogLevel = logger.Info
	}
	newLogger := logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), loggerConfig)

	if os.Getenv("POSTGRESQL_URI") != "" {
		dbc, err = gorm.Open(postgres.Open(os.Getenv("POSTGRESQL_URI")), &gorm.Config{Logger: newLogger})
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
