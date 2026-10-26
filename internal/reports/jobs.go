package reports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// StartWorker resumes queued work after a platform restart and processes one
// report at a time. Jobs are persisted so leaving the browser never cancels a
// model call or loses its outcome.
func (s *Service) StartWorker(ctx context.Context) {
	s.workerOnce.Do(func() {
		_, _ = s.db.ExecContext(ctx, `UPDATE operational_report_jobs SET status='queued',stage='queued',message='平台重启后已恢复排队',started_at='' WHERE status='running'`)
		go s.runJobs(ctx)
	})
}

func (s *Service) Submit(ctx context.Context, request Request, now time.Time) (Job, error) {
	if _, err := PeriodWindow(request.Period, request.Date, now); err != nil {
		return Job{}, err
	}
	if len(request.Environment) > 64 || len(request.ClusterType) > 32 {
		return Job{}, errors.New("筛选范围无效")
	}
	if request.ClusterType != "" && request.ClusterType != "G01" && request.ClusterType != "OCP" && request.ClusterType != "未标记" {
		return Job{}, errors.New("资源池类型无效")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Job{}, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Job{}, err
	}
	job := Job{ID: hex.EncodeToString(idBytes), Status: "queued", Stage: "queued", Message: "已提交，等待后台执行", Request: request, CreatedAt: now.UTC()}
	_, err = s.db.ExecContext(ctx, `INSERT INTO operational_report_jobs(id,status,stage,message,error_message,report_id,request_json,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, job.ID, job.Status, job.Stage, job.Message, "", "", string(raw), job.CreatedAt.Format(time.RFC3339Nano), "", "")
	if err != nil {
		return Job{}, err
	}
	s.wakeWorker()
	return job, nil
}

func (s *Service) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,status,stage,message,error_message,report_id,request_json,created_at,started_at,finished_at FROM operational_report_jobs ORDER BY created_at DESC,id DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Service) GetJob(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,status,stage,message,error_message,report_id,request_json,created_at,started_at,finished_at FROM operational_report_jobs WHERE id=?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	return job, err
}

type jobScanner interface{ Scan(...any) error }

func scanJob(row jobScanner) (Job, error) {
	var job Job
	var raw, created, started, finished string
	if err := row.Scan(&job.ID, &job.Status, &job.Stage, &job.Message, &job.ErrorMessage, &job.ReportID, &raw, &created, &started, &finished); err != nil {
		return job, err
	}
	if err := json.Unmarshal([]byte(raw), &job.Request); err != nil {
		return job, err
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	job.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	return job, nil
}

func (s *Service) wakeWorker() {
	select {
	case s.jobSignal <- struct{}{}:
	default:
	}
}

func (s *Service) runJobs(ctx context.Context) {
	for {
		processed, err := s.processNextJob(ctx)
		if err != nil && ctx.Err() != nil {
			return
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.jobSignal:
		case <-time.After(30 * time.Second):
		}
	}
}

func (s *Service) processNextJob(ctx context.Context) (bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM operational_report_jobs WHERE status='queued' ORDER BY created_at,id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE operational_report_jobs SET status='running',stage='collecting',message='正在汇总已保存的告警、Event、容量与安全数据',started_at=? WHERE id=? AND status='queued'`, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return false, err
	}
	claimed, _ := result.RowsAffected()
	if claimed == 0 {
		return true, nil
	}
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return true, err
	}
	report, generateErr := s.generateWithProgress(ctx, job.Request, time.Now(), func(stage, message string) {
		_, _ = s.db.ExecContext(context.Background(), `UPDATE operational_report_jobs SET stage=?,message=? WHERE id=? AND status='running'`, stage, message, id)
	})
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	if generateErr != nil {
		message := publicGenerationError(generateErr)
		_, err = s.db.ExecContext(context.Background(), `UPDATE operational_report_jobs SET status='failed',stage='failed',message='报告生成失败',error_message=?,finished_at=? WHERE id=?`, message, finished, id)
		return true, err
	}
	message := "报告已生成"
	if report.AIStatus == "failed" {
		message = "事实报告已生成，AI 分析未完成"
	}
	_, err = s.db.ExecContext(context.Background(), `UPDATE operational_report_jobs SET status='completed',stage='completed',message=?,error_message=?,report_id=?,finished_at=? WHERE id=?`, message, report.AIMessage, report.ID, finished, id)
	return true, err
}

func publicGenerationError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}
