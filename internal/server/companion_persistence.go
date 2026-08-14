package server

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sort"
	"sync"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/storage"
)

type companionPersistence struct {
	store        storage.CompanionStore
	config       Config
	mu           sync.Mutex
	completionMu sync.Mutex
	records      []companion.Body
	persisted    uint64
	dirty        bool
	inFlight     bool
	inFlightJob  companionSaveJob
	retry        *companionSaveJob
	jobs         chan companionSaveJob
	completions  chan companionSaveCompletion
	ctx          context.Context
	cancel       context.CancelFunc
	waitGroup    sync.WaitGroup
	closed       bool
	closeOnce    sync.Once
}

type companionSaveJob struct {
	Save     storage.CompanionSave
	Attempt  uint32
	NextTick uint64
}

type companionSaveCompletion struct {
	Job companionSaveJob
	Err error
}

func newCompanionPersistence(
	store storage.CompanionStore,
	loaded storage.StoredCompanions,
	config Config,
) *companionPersistence {
	ctx, cancel := context.WithCancel(context.Background())
	persistence := &companionPersistence{
		store:       store,
		config:      config,
		records:     cloneAndSortCompanionBodies(loaded.Records),
		persisted:   loaded.Revision,
		jobs:        make(chan companionSaveJob, 1),
		completions: make(chan companionSaveCompletion, 1),
		ctx:         ctx,
		cancel:      cancel,
	}
	persistence.waitGroup.Add(1)
	go persistence.worker()
	return persistence
}

func (p *companionPersistence) Observe(active []companion.Body) {
	p.mu.Lock()
	defer p.mu.Unlock()
	byID := make(map[companion.ID]companion.Body, len(p.records)+len(active))
	for _, body := range p.records {
		byID[body.ID] = body
	}
	for _, body := range active {
		byID[body.ID] = body
	}
	if len(byID) > companion.MaxStored {
		panic("server: companion persistence exceeds stored record limit")
	}
	records := make([]companion.Body, 0, len(byID))
	for _, body := range byID {
		records = append(records, body)
	}
	sortCompanionBodies(records)
	if slices.Equal(records, p.records) {
		return
	}
	p.records = records
	p.dirty = true
}

func (p *companionPersistence) Poll(tick uint64) error {
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()

	var result error
	for {
		select {
		case completion := <-p.completions:
			if err := p.applyCompletionLocked(completion, tick); err != nil {
				result = errors.Join(result, err)
			}
		default:
			goto drained
		}
	}

drained:
	if p.inFlight || p.closed {
		return result
	}
	if p.retry != nil {
		if p.retry.NextTick <= tick {
			job := cloneCompanionSaveJob(*p.retry)
			if p.dispatchLocked(job) {
				p.retry = nil
			}
		}
		return result
	}
	if p.dirty && tick%p.config.AutosaveTicks == 0 {
		p.dispatchLocked(p.latestJobLocked())
	}
	return result
}

func (p *companionPersistence) Flush(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil companion flush context")
	}
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	p.mu.Lock()
	inherited := p.inFlight
	hasRetry := !inherited && p.retry != nil
	hasDirty := p.dirty
	p.mu.Unlock()

	switch {
	case inherited:
		if err := p.waitForInflight(ctx); err != nil {
			return err
		}
	case hasRetry:
		if err := p.dispatchAndWait(ctx, true); err != nil {
			return err
		}
	case hasDirty:
		if err := p.dispatchAndWait(ctx, false); err != nil {
			return err
		}
	default:
		return nil
	}

	p.mu.Lock()
	dirty := p.dirty
	p.mu.Unlock()
	if !dirty {
		return nil
	}
	return p.dispatchAndWait(ctx, false)
}

func (p *companionPersistence) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.cancel()
		p.waitGroup.Wait()
	})
}

func (p *companionPersistence) worker() {
	defer p.waitGroup.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.jobs:
			err := p.store.SaveCompanions(p.ctx, cloneCompanionSave(job.Save))
			select {
			case p.completions <- companionSaveCompletion{Job: job, Err: err}:
			case <-p.ctx.Done():
				return
			}
		}
	}
}

func (p *companionPersistence) dispatchAndWait(ctx context.Context, retry bool) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return p.ctx.Err()
	}
	var job companionSaveJob
	if retry {
		if p.retry == nil {
			p.mu.Unlock()
			return nil
		}
		job = cloneCompanionSaveJob(*p.retry)
	} else {
		if !p.dirty {
			p.mu.Unlock()
			return nil
		}
		job = p.latestJobLocked()
	}
	if !p.dispatchLocked(job) {
		p.mu.Unlock()
		return nil
	}
	if retry {
		p.retry = nil
	}
	p.mu.Unlock()
	return p.waitForInflight(ctx)
}

func (p *companionPersistence) waitForInflight(ctx context.Context) error {
	for {
		select {
		case completion := <-p.completions:
			p.mu.Lock()
			matched := p.inFlight &&
				p.inFlightJob.Save.Revision == completion.Job.Save.Revision
			err := p.applyCompletionLocked(completion, 0)
			p.mu.Unlock()
			if matched {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-p.ctx.Done():
			return p.ctx.Err()
		}
	}
}

func (p *companionPersistence) dispatchLocked(job companionSaveJob) bool {
	if p.closed || p.inFlight {
		return false
	}
	queued := cloneCompanionSaveJob(job)
	select {
	case p.jobs <- queued:
		p.inFlight = true
		p.inFlightJob = cloneCompanionSaveJob(job)
		return true
	default:
		return false
	}
}

func (p *companionPersistence) applyCompletionLocked(
	completion companionSaveCompletion,
	tick uint64,
) error {
	if !p.inFlight || p.inFlightJob.Save.Revision != completion.Job.Save.Revision {
		return nil
	}
	p.inFlight = false
	p.inFlightJob = companionSaveJob{}
	if completion.Err != nil {
		retry := cloneCompanionSaveJob(completion.Job)
		attempt := retry.Attempt
		if attempt == 0 {
			attempt = 1
		}
		retry.NextTick = saturatingAddUint64(
			tick,
			retryDelay(p.config.RetryBaseTicks, p.config.RetryMaxTicks, attempt),
		)
		if attempt < ^uint32(0) {
			retry.Attempt = attempt + 1
		}
		p.retry = &retry
		p.dirty = true
		return completion.Err
	}
	p.persisted = completion.Job.Save.Revision
	p.retry = nil
	p.dirty = !slices.Equal(p.records, completion.Job.Save.Records)
	return nil
}

func (p *companionPersistence) latestJobLocked() companionSaveJob {
	return companionSaveJob{
		Save: storage.CompanionSave{
			Revision: p.persisted + 1,
			Records:  slices.Clone(p.records),
		},
		Attempt: 1,
	}
}

func cloneCompanionSaveJob(job companionSaveJob) companionSaveJob {
	job.Save = cloneCompanionSave(job.Save)
	return job
}

func cloneCompanionSave(save storage.CompanionSave) storage.CompanionSave {
	save.Records = slices.Clone(save.Records)
	return save
}

func cloneAndSortCompanionBodies(records []companion.Body) []companion.Body {
	clone := slices.Clone(records)
	sortCompanionBodies(clone)
	return clone
}

func sortCompanionBodies(records []companion.Body) {
	sort.Slice(records, func(left, right int) bool {
		return bytes.Compare(records[left].ID[:], records[right].ID[:]) < 0
	})
}
