package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"

	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"6.824/labgob"
	"6.824/labrpc"
)

//
// A Go object implementing a single Raft peer.
//

const (
	Follower            = "Follower"
	Candidate           = "Candidate"
	Leader              = "Leader"
	HeartbeatTimeout    = time.Millisecond * 100
	BaseElectionTimeout = time.Millisecond * 300
)

type Entry struct {
	Term    int
	Command interface{}
}

//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in part 2D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 2D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	// A
	state         string
	currentTerm   int
	voteFor       int
	electionTimer *time.Timer
	// B
	logs           []Entry
	appleCh        chan ApplyMsg
	commitIndex    int
	lastApplied    int
	nextIndex      []int
	matchIndex     []int
	heartbeatTimer *time.Timer
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		peers:     peers,
		persister: persister,
		me:        me,
		dead:      0,

		// Your initialization code here (2A, 2B, 2C).
		state:         Follower,
		currentTerm:   0,
		voteFor:       -1,
		electionTimer: time.NewTimer(getElectionTimeout()),

		logs:           make([]Entry, 1),
		appleCh:        applyCh,
		nextIndex:      make([]int, len(peers)),
		matchIndex:     make([]int, len(peers)),
		heartbeatTimer: time.NewTimer(HeartbeatTimeout),
	}

	// start ticker goroutine to start elections
	go rf.ticker()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// for i := 0; i < len(rf.peers); i++ {
	// 	rf.nextIndex[i] = len(rf.logs)
	// 	rf.matchIndex[i] = 0
	// }
	return rf
}

func getElectionTimeout() time.Duration {
	t := BaseElectionTimeout + time.Duration(rand.Int63())%BaseElectionTimeout
	return t
}

// func getHeartbeatTimeout() time.Duration {
// 	t := BaseElectionTimeout + time.Duration(rand.Int63())%BaseElectionTimeout
// 	return t
// }

// The ticker go routine starts a new election if this peer hasn't received
// heartsbeats recently.
func (rf *Raft) ticker() {
	// 判断本节点是否活着
	for rf.killed() == false {
		// Your code here to check if a leader election should
		// be started and to randomize sleeping time using
		// time.Sleep().
		select {
		case <-rf.electionTimer.C:
			rf.mu.Lock()
			rf.transStatus(Candidate)
			rf.mu.Unlock()
		case <-rf.heartbeatTimer.C:
			rf.mu.Lock()
			if rf.state == Leader {
				rf.broadcastHeartbeat()
				rf.heartbeatTimer.Reset(HeartbeatTimeout)
			}
			rf.mu.Unlock()
		}
	}
}

func (rf *Raft) transStatus(s string) {
	// if s == rf.state {
	// 	return
	// }
	rf.state = s
	switch s {
	case Follower:
		rf.heartbeatTimer.Stop()
		rf.voteFor = -1
		rf.electionTimer.Reset(getElectionTimeout())
	case Candidate:
		//fmt.Printf("%d-%d 的选举计时过期，变成candidate", rf.currentTerm, rf.me)
		rf.startElection()
	case Leader:
		for i := 0; i < len(rf.peers); i++ {
			// 从最后一条开始试
			rf.nextIndex[i] = len(rf.logs)
			rf.matchIndex[i] = 0
		}
		//log.Printf("%d-%dtrans to leader:%v", rf.currentTerm, rf.me, rf.nextIndex)
		rf.electionTimer.Stop()
		rf.broadcastHeartbeat()
		rf.heartbeatTimer.Reset(HeartbeatTimeout)
	}
}

func (rf *Raft) startElection() {
	defer rf.persist()
	rf.currentTerm += 1
	rf.electionTimer.Reset(getElectionTimeout())
	args := RequestVoteArgs{
		Term:         rf.currentTerm,
		CandidateId:  rf.me,
		LastLogIndex: len(rf.logs) - 1,
		LastLogTerm:  rf.logs[len(rf.logs)-1].Term,
	}
	voteCount := 0
	//var voteNote []string
	//fmt.Printf("cand%d-%d vote, argterm%d", rf.currentTerm, rf.me, args.Term)
	for i := range rf.peers {
		if i == rf.me {
			voteCount += 1
			rf.voteFor = rf.me
			continue
		}

		go func(peer int) {
			var reply RequestVoteReply
			//log.Printf("gocand%d-%d,to%d, argterm%d,vote%d/%d", rf.currentTerm, rf.me, peer, args.Term, voteCount, len(rf.peers))
			if rf.sendRequestVote(peer, &args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				//log.Printf("sendcand%d-%d,to%d-%d, argterm%d,vote%d/%d, got?%v", rf.currentTerm, rf.me, reply.Term, peer, args.Term, voteCount, len(rf.peers), reply.GrantedVote)
				if reply.Term > rf.currentTerm {
					rf.currentTerm = reply.Term
					rf.transStatus(Follower)
					rf.persist()
				}
				if reply.GrantedVote && rf.state == Candidate {
					voteCount += 1
					//s := fmt.Sprintf("%d-%d", reply.Term, reply.WhoVoted)
					//voteNote = append(voteNote, s)
					if voteCount > len(rf.peers)/2 {
						rf.transStatus(Leader)
						//fmt.Printf("term%d server%d,拿了%v的%d/%d票,变成了%s", rf.currentTerm, rf.me, voteNote, voteCount, len(rf.peers), rf.state)
					}
				}
			}
		}(i)
	}
}

//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateId  int
	LastLogTerm  int // 2B
	LastLogIndex int // 2B
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int
	GrantedVote bool
	WhoVoted    int
	WhoState    string
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist() // defer 先进后出
	//fmt.Printf("%d-%d要求%d-%d-%s投票,它的票%d\n", args.Term, args.CandidateId, rf.currentTerm, rf.me, rf.state, rf.voteFor)
	reply.WhoVoted = rf.me
	reply.WhoState = rf.state

	inNewerTerm := args.Term < rf.currentTerm
	alreadyVoted := args.Term == rf.currentTerm && rf.voteFor != -1 && rf.voteFor != args.CandidateId
	if inNewerTerm || alreadyVoted {
		reply.Term = rf.currentTerm
		reply.GrantedVote = false
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.transStatus(Follower)
	}

	// 2B
	termTooSmall := args.LastLogTerm < rf.logs[len(rf.logs)-1].Term
	indexTooSmall := args.LastLogTerm == rf.logs[len(rf.logs)-1].Term && args.LastLogIndex < len(rf.logs)-1
	if termTooSmall || indexTooSmall {
		reply.Term = rf.currentTerm
		reply.GrantedVote = false
		return
	}

	reply.GrantedVote = true
	reply.Term = args.Term
	rf.voteFor = args.CandidateId
	rf.electionTimer.Reset(getElectionTimeout())
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	LogEntries   []Entry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term          int
	Success       bool
	ConflictTerm  int // 2C
	ConflictIndex int // 2C
}

func (rf *Raft) broadcastHeartbeat() {
	for peer := range rf.peers {
		if peer == rf.me {
			// rf.nextIndex[peer] = len(rf.logs)
			// rf.matchIndex[peer] = len(rf.logs) - 1
			continue
		}
		go rf.replicateEntry(peer)
	}
}

func (rf *Raft) replicateEntry(peer int) {
	rf.mu.Lock()
	if rf.state != Leader {
		rf.mu.Unlock()
		return
	}
	prevLogIndex := rf.nextIndex[peer] - 1
	//log.Printf("%d-%d-len(%d)，给%d-len(%d)发广播", rf.currentTerm, rf.me, len(rf.logs), peer, prevLogIndex+1)
	args := AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderId:     rf.me,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  rf.logs[prevLogIndex].Term,
		LogEntries:   rf.logs[rf.nextIndex[peer]:],
		LeaderCommit: rf.commitIndex,
	}
	rf.mu.Unlock()

	var reply AppendEntriesReply
	if rf.sendAppendEntries(peer, &args, &reply) {
		rf.mu.Lock()
		if rf.state != Leader {
			rf.mu.Unlock()
			return
		}
		//log.Printf("%d-%dsendapply,nextIndex%v", rf.currentTerm, rf.me, rf.nextIndex)
		if reply.Success {
			rf.matchIndex[peer] = args.PrevLogIndex + len(args.LogEntries)
			rf.nextIndex[peer] = rf.matchIndex[peer] + 1
			for index := len(rf.logs) - 1; index > rf.commitIndex; index-- {
				count := 0
				for _, matchIdx := range rf.matchIndex {
					if matchIdx >= index {
						count += 1
					}
				}
				if count > len(rf.peers)/2 {
					rf.setCommitIndex(index)
					break
				}
			}
		} else {
			if reply.Term > rf.currentTerm {
				rf.currentTerm = reply.Term
				rf.transStatus(Follower)
				rf.persist()
			} else {
				//rf.nextIndex[peer] = args.PrevLogIndex - 1
				rf.nextIndex[peer] = reply.ConflictIndex
				if reply.ConflictTerm != -1 {
					for i := args.PrevLogIndex; i >= 1; i-- {
						if rf.logs[i-1].Term == reply.ConflictTerm {
							rf.nextIndex[peer] = i
							break
						}
					}
				}
			}
		}
		//log.Printf("%d-%dsendfinish,nextIndex%v", rf.currentTerm, rf.me, rf.nextIndex)
		rf.mu.Unlock()
	}
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	//log.Printf("entry: 领导:%d-%d last%d-%d, 本机%d-%d last%d-%d", args.Term, args.LeaderId, args.PrevLogTerm, args.PrevLogIndex, rf.currentTerm, rf.me, rf.logs[len(rf.logs)-1].Term, len(rf.logs)-1)
	// 判断任期：小于本机任期，直接失败
	if args.Term < rf.currentTerm {
		reply.Success = false
		reply.Term = rf.currentTerm
		return
	}
	// 判断任期：确是更大任期，本机直接转为follower
	if args.Term > rf.currentTerm {
		reply.Term = args.Term
		rf.transStatus(Follower)
	}

	// 如果term相同要重置时间吗？
	rf.electionTimer.Reset(getElectionTimeout())

	// 判断是否接收数据：index/term不匹配代表不一致
	//（log doesn’t contain an entry at prevLogIndex）
	if len(rf.logs)-1 < args.PrevLogIndex {
		reply.Success = false
		reply.Term = rf.currentTerm
		reply.ConflictTerm = -1
		reply.ConflictIndex = len(rf.logs)
		return
	}
	if rf.logs[args.PrevLogIndex].Term != args.PrevLogTerm {
		reply.Success = false
		reply.Term = rf.currentTerm
		reply.ConflictTerm = rf.logs[args.PrevLogIndex].Term
		for i := 1; i <= args.PrevLogIndex; i++ {
			if rf.logs[i].Term == reply.ConflictTerm {
				reply.ConflictIndex = i
				return
			}
		}
		return
	}

	// if args.LogEntries == nil || len(args.LogEntries) <= 0 {
	// 	// 日志为空，说明这是心跳
	// 	return
	// }

	// 复制entries
	for i, entry := range args.LogEntries {
		index := args.PrevLogIndex + i + 1
		if index > len(rf.logs)-1 {
			rf.logs = append(rf.logs, entry)
		} else {
			if rf.logs[index].Term != entry.Term {
				rf.logs = rf.logs[:index]
				rf.logs = append(rf.logs, entry)
			}
		}
	}

	// 判断是否提交
	if args.LeaderCommit > rf.commitIndex {
		rf.setCommitIndex(min(args.LeaderCommit, len(rf.logs)-1))
	}
	reply.Success = true
}

func (rf *Raft) setCommitIndex(commitIndex int) {
	rf.commitIndex = commitIndex
	if rf.commitIndex > rf.lastApplied {
		entriesToApply := append([]Entry{}, rf.logs[(rf.lastApplied+1):(rf.commitIndex+1)]...)
		go func(startIdx int, entries []Entry) {
			for idx, entry := range entries {
				msg := ApplyMsg{
					CommandValid: true,
					Command:      entry.Command,
					CommandIndex: startIdx + idx,
				}
				rf.appleCh <- msg
				rf.mu.Lock()
				rf.lastApplied = max(rf.lastApplied, msg.CommandIndex)
				rf.mu.Unlock()
			}
		}(rf.lastApplied+1, entriesToApply)
	}
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	var term int
	var isleader bool
	// Your code here (2A).
	rf.mu.Lock()
	term = rf.currentTerm
	isleader = rf.state == Leader
	rf.mu.Unlock()
	//log.Printf("seesee server %d 的state：term%d，role%s", rf.me, rf.currentTerm, rf.state)
	return term, isleader
}

//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	// term := -1
	// isLeader := true

	// Your code here (2B).
	term, isLeader := rf.GetState()
	if isLeader {
		rf.mu.Lock()
		rf.logs = append(rf.logs, Entry{Command: command, Term: term})
		index = len(rf.logs) - 1
		rf.matchIndex[rf.me] = index
		rf.nextIndex[rf.me] = index + 1
		rf.persist()
		//log.Printf("START:leader%d-%d-len(%d) nextIndex%v", rf.currentTerm, rf.me, index, rf.nextIndex)
		//rf.broadcastHeartbeat()
		rf.mu.Unlock()
	}
	// term 没有用到
	return index, term, isLeader
}

//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.currentTerm)
	e.Encode(rf.voteFor)
	e.Encode(rf.logs)
	rf.persister.SaveRaftState(w.Bytes())
}

//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm, voteFor int
	var logs []Entry
	if d.Decode(&currentTerm) != nil ||
		d.Decode(&voteFor) != nil ||
		d.Decode((&logs)) != nil {
		DPrintf("%v fails to recover from persist", rf)
		return
	}

	rf.currentTerm = currentTerm
	rf.voteFor = voteFor
	rf.logs = logs
}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}

//
// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
//
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func min(x, y int) int {
	if x < y {
		return x
	} else {
		return y
	}
}

func max(x, y int) int {
	if x > y {
		return x
	} else {
		return y
	}
}
