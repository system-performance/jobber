package main

import (
	"context"
	"github.com/dshearer/jobber/common"
	"github.com/dshearer/jobber/jobfile"
	"os/exec"
	"sync"
	"time"
)

type JobRunnerThread struct {
	running            bool
	runRecChan         chan *jobfile.RunRec
	mainThreadDoneChan chan interface{}
	ctxCancel          context.CancelFunc
}

func NewJobRunnerThread() *JobRunnerThread {
	return &JobRunnerThread{
		running: false,
	}
}

func (self *JobRunnerThread) RunRecChan() <-chan *jobfile.RunRec {
	return self.runRecChan
}

func (self *JobRunnerThread) Start(
	ctx context.Context,
	jobs []*jobfile.Job,
	shell string) {

	if self.running {
		panic("JobRunnerThread already running.")
	}
	self.running = true

	self.mainThreadDoneChan = make(chan interface{})

	// make subcontext
	subCtx, cancel := context.WithCancel(ctx)
	self.ctxCancel = cancel

	self.runRecChan = make(chan *jobfile.RunRec)
	var jobQ JobQueue
	jobQ.SetJobs(time.Now(), jobs)

	go func() {
		defer close(self.mainThreadDoneChan)

		var jobThreadWaitGroup sync.WaitGroup

		for {
			var job *jobfile.Job = jobQ.Pop(subCtx, time.Now()) // sleeps

			if job != nil && !job.Paused {
				// launch thread to run this job
				common.Logger.Printf("%v: %v\n", job.User, job.Cmd)
				jobThreadWaitGroup.Add(1)
				go func(job *jobfile.Job) {
					defer jobThreadWaitGroup.Done()
					self.runRecChan <- RunJob(job, shell, false)
				}(job)

			} else if job == nil {
				/* We were canceled. */
				//Logger.Printf("Run thread got 'stop'\n")
				break
			}
		}

		// wait for run threads to stop
		//Logger.Printf("JobRunner: cleaning up...\n")
		jobThreadWaitGroup.Wait()

		// close run-rec channel
		close(self.runRecChan)
		//Logger.Printf("JobRunner done\n")
	}()
}

func (self *JobRunnerThread) Cancel() {
	self.ctxCancel()
	self.running = false
}

func (self *JobRunnerThread) Wait() {
	<-self.mainThreadDoneChan
}

func RunJob(
	job *jobfile.Job,
	shell string,
	testing bool) *jobfile.RunRec {

	rec := &jobfile.RunRec{Job: job, RunTime: time.Now()}

	// run
	var execResult *common.ExecResult
	execResult, err :=
		common.ExecAndWait(exec.Command(shell, "-c", job.Cmd), nil)

	if err != nil {
		/* unexpected error while trying to run job */
		common.Logger.Printf("RunJob: %v", err)
		rec.Err = err
		return rec
	}

	// update run rec
	rec.Succeeded = execResult.Succeeded
	rec.NewStatus = jobfile.JobGood
	rec.Stdout = &execResult.Stdout
	rec.Stderr = &execResult.Stderr

	if !testing {
		// update job
		if execResult.Succeeded {
			/* job succeeded */
			job.Status = jobfile.JobGood
		} else {
			/* job failed: apply error-handler (which sets job.Status) */
			job.ErrorHandler.Apply(job)
		}
		job.LastRunTime = rec.RunTime

		// update rec.NewStatus
		rec.NewStatus = job.Status
	}

	return rec
}
