package surriti

import (
	"context"
	"sync"
	"time"
)

type CognitionGroupState struct {
	GroupID string
	PendingEpisodeUUIDs []string
	LastNotifyAt time.Time
	PassCount int
}

func (s *CognitionGroupState) MarkDirty(episodeUUID string) {
	if episodeUUID!="" && !containsString(s.PendingEpisodeUUIDs,episodeUUID){
		s.PendingEpisodeUUIDs=append(s.PendingEpisodeUUIDs,episodeUUID)
	}
	s.LastNotifyAt=time.Now()
}

func (s *CognitionGroupState) Take(limit int) []string {
	if limit<=0||limit>len(s.PendingEpisodeUUIDs){limit=len(s.PendingEpisodeUUIDs)}
	out:=append([]string(nil),s.PendingEpisodeUUIDs[:limit]...)
	s.PendingEpisodeUUIDs=append([]string(nil),s.PendingEpisodeUUIDs[limit:]...)
	s.PassCount++
	return out
}

func (s *CognitionGroupState) Retry(episodeUUIDs []string) {
	seen:=map[string]struct{}{}
	out:=make([]string,0,len(episodeUUIDs)+len(s.PendingEpisodeUUIDs))
	for _,u:=range episodeUUIDs{if u!=""{if _,ok:=seen[u];!ok{seen[u]=struct{}{};out=append(out,u)}}}
	for _,u:=range s.PendingEpisodeUUIDs{if u!=""{if _,ok:=seen[u];!ok{seen[u]=struct{}{};out=append(out,u)}}}
	s.PendingEpisodeUUIDs=out
}

type CognitionScheduler struct {
	driver Queryer
	llm LLMClient
	embedder Embedder
	config CognitionConfig

	mu sync.Mutex
	states map[string]*CognitionGroupState
	timers map[string]*time.Timer
	generation map[string]uint64
	running map[string]bool
	groupLocks map[string]*sync.Mutex
	stopped bool

	sem chan struct{}
	ctx context.Context
	cancel context.CancelFunc
	wg sync.WaitGroup
}

func NewCognitionScheduler(driver Queryer,llm LLMClient,embedder Embedder,config CognitionConfig)*CognitionScheduler{
	ctx,cancel:=context.WithCancel(context.Background())
	n:=config.MaxConcurrentGroups;if n<1{n=1}
	return &CognitionScheduler{
		driver:driver,llm:llm,embedder:embedder,config:config,
		states:map[string]*CognitionGroupState{},timers:map[string]*time.Timer{},generation:map[string]uint64{},
		running:map[string]bool{},groupLocks:map[string]*sync.Mutex{},
		sem:make(chan struct{},n),ctx:ctx,cancel:cancel,
	}
}

func (s *CognitionScheduler) Enabled()bool{return s!=nil&&s.config.Enabled}

func (s *CognitionScheduler) Start(){
	if s==nil{return}
	s.mu.Lock();defer s.mu.Unlock()
	if !s.stopped{return}
	s.ctx,s.cancel=context.WithCancel(context.Background());s.stopped=false
}

func (s *CognitionScheduler) stateLocked(groupID string)*CognitionGroupState{
	st:=s.states[groupID]
	if st==nil{st=&CognitionGroupState{GroupID:groupID,PendingEpisodeUUIDs:[]string{}};s.states[groupID]=st}
	return st
}

func (s *CognitionScheduler) groupLockLocked(groupID string)*sync.Mutex{
	l:=s.groupLocks[groupID];if l==nil{l=&sync.Mutex{};s.groupLocks[groupID]=l};return l
}

func (s *CognitionScheduler) Notify(groupID,episodeUUID string){
	if s==nil||!s.config.Enabled{return}
	s.mu.Lock()
	if s.stopped{s.mu.Unlock();return}
	st:=s.stateLocked(groupID);st.MarkDirty(episodeUUID)
	delay:=time.Duration(s.config.IdleSeconds*float64(time.Second))
	if len(st.PendingEpisodeUUIDs)>=s.config.BatchThreshold{delay=0}
	s.scheduleLocked(groupID,delay)
	s.mu.Unlock()
}

func (s *CognitionScheduler) scheduleLocked(groupID string,delay time.Duration){
	if t:=s.timers[groupID];t!=nil{t.Stop()}
	s.generation[groupID]++
	gen:=s.generation[groupID]
	s.timers[groupID]=time.AfterFunc(delay,func(){
		s.mu.Lock()
		if s.stopped||s.generation[groupID]!=gen{s.mu.Unlock();return}
		delete(s.timers,groupID)
		if s.running[groupID]{s.mu.Unlock();return}
		s.running[groupID]=true
		s.wg.Add(1)
		s.mu.Unlock()
		go func(){
			defer s.wg.Done()
			s.runGroup(s.ctx,groupID)
			s.mu.Lock()
			s.running[groupID]=false
			st:=s.states[groupID]
			if !s.stopped&&st!=nil&&len(st.PendingEpisodeUUIDs)>0{s.scheduleLocked(groupID,0)}
			s.mu.Unlock()
		}()
	})
}

func (s *CognitionScheduler) runGroup(ctx context.Context,groupID string){
	s.mu.Lock();lock:=s.groupLockLocked(groupID);s.mu.Unlock()
	lock.Lock();defer lock.Unlock()
	select{case s.sem<-struct{}{}:defer func(){<-s.sem}();case <-ctx.Done():return}
	for{
		s.mu.Lock()
		if s.stopped&&ctx.Err()!=nil{s.mu.Unlock();return}
		st:=s.stateLocked(groupID)
		limit:=s.config.MaxEpisodesPerPass;if limit<1{limit=1}
		batch:=st.Take(limit);passCount:=st.PassCount
		s.mu.Unlock()
		if len(batch)==0{return}
		metrics:=RunCognitionPass(ctx,s.driver,s.llm,s.embedder,groupID,batch,s.config,passCount)
		if len(metrics.FailedSteps)>0{
			s.mu.Lock();s.stateLocked(groupID).Retry(batch);s.mu.Unlock();return
		}
		s.mu.Lock();pending:=len(s.stateLocked(groupID).PendingEpisodeUUIDs)>0;s.mu.Unlock()
		if !pending{return}
	}
}

func (s *CognitionScheduler) RunOnce(ctx context.Context,groupID string,episodeUUIDs []string) CognitionMetrics {
	if s==nil||!s.config.Enabled{return CognitionMetrics{GroupID:groupID}}
	s.mu.Lock()
	if t:=s.timers[groupID];t!=nil{t.Stop();delete(s.timers,groupID)}
	st:=s.stateLocked(groupID);for _,u:=range episodeUUIDs{st.MarkDirty(u)}
	lock:=s.groupLockLocked(groupID)
	s.mu.Unlock()

	lock.Lock();defer lock.Unlock()
	select{case s.sem<-struct{}{}:defer func(){<-s.sem}();case <-ctx.Done():return CognitionMetrics{GroupID:groupID,FailedSteps:[]string{"cancelled"}}}
	s.mu.Lock();limit:=s.config.MaxEpisodesPerPass;if limit<1{limit=1};batch:=st.Take(limit);passCount:=st.PassCount;s.mu.Unlock()
	if len(batch)==0{return CognitionMetrics{GroupID:groupID}}
	metrics:=RunCognitionPass(ctx,s.driver,s.llm,s.embedder,groupID,batch,s.config,passCount)
	if len(metrics.FailedSteps)>0{s.mu.Lock();st.Retry(batch);s.mu.Unlock()}
	return metrics
}

func (s *CognitionScheduler) RecoverPendingEpisodes(ctx context.Context)(int,error){
	if s==nil||!s.config.Enabled{return 0,nil}
	recovered:=0
	var cursorCreated *time.Time
	cursorUUID:=""
	pageSize:=s.config.MaxEpisodesPerPass*8;if pageSize<1{pageSize=8}
	for{
		filter:="";params:=map[string]any{"limit":pageSize}
		if cursorCreated!=nil{
			filter=" AND (created_at > $cursor_created_at OR (created_at = $cursor_created_at AND uuid > $cursor_uuid))"
			params["cursor_created_at"]=*cursorCreated;params["cursor_uuid"]=cursorUUID
		}
		raw,err:=s.driver.Query(ctx,`
SELECT group_id, uuid, created_at
FROM episode
WHERE cognition_processed_at IS NONE AND ingestion_complete = true`+filter+`
ORDER BY created_at ASC, uuid ASC
LIMIT $limit;`,params)
		if err!=nil{return recovered,err}
		rows:=UnwrapRows(raw);if len(rows)==0{break}
		for _,row:=range rows{u:=stringFromAny(row["uuid"]);if u==""{continue};s.Notify(stringFromAny(row["group_id"]),u);recovered++}
		last:=rows[len(rows)-1];cursorCreated=coerceTime(last["created_at"]);cursorUUID=stringFromAny(last["uuid"])
		if cursorCreated==nil||len(rows)<pageSize{break}
		select{case <-ctx.Done():return recovered,ctx.Err();default:}
	}
	return recovered,nil
}

func (s *CognitionScheduler) Shutdown(ctx context.Context) {
	if s==nil{return}
	s.mu.Lock()
	if s.stopped{s.mu.Unlock();return}
	s.stopped=true
	for group,t:=range s.timers{if t!=nil{t.Stop()};delete(s.timers,group);s.generation[group]++}
	s.mu.Unlock()

	done:=make(chan struct{})
	go func(){s.wg.Wait();close(done)}()
	timer:=time.NewTimer(2*time.Second);defer timer.Stop()
	select{
	case <-done:
		s.cancel()
		return
	case <-timer.C:
		s.cancel()
	case <-ctx.Done():
		s.cancel()
	}
	select{case <-done:case <-ctx.Done():}
}
