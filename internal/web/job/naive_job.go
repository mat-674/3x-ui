package job

import (
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/naive"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type NaiveJob struct {
	inboundService service.InboundService
}

func NewNaiveJob() *NaiveJob {
	return new(NaiveJob)
}

func (j *NaiveJob) Run() {
	desired, err := j.inboundService.DesiredNaiveInstances()
	if err != nil {
		logger.Warning("naive job: get desired instances failed:", err)
		return
	}
	naive.GetManager().Reconcile(desired)
}
