LAB2 笔记.md

build 一个容错的keyvalue storage系统
raft 动画： http://thesecretlivesofdata.com/raft/
https://www.jianshu.com/p/5aed73b288f7

A: 实现raft：一个replicated state machine protocol 复制状态机协议
B: 实现日志传输
C：持久化


1. raft 

    - raft维护server集群，每个集群有三种状态：follower、candidate、leader，三种状态可以互相转换，相当于一个状态机。
    状态机：(那张图)
        · follower：每个s初始化时都是follower。
            -> 进来后，停止发心跳，投票置-1，选举定时reset
            如果超时-> candidate
        · candidate：
            -> 进来后就开始选举
            已经有新leader或到新term->follower;
            选举超时-> candidate; 
            选举成功-> leader;
        · leader:
            -> 进来后就停止计时，开始广播，重置心跳计时
            如果有更高的term-> follower;
    
    - term: 一个任期。和拟人态的选举任期一个意思。
        type：这里term是个数值，表示第几任，每个server都存着当前的任期。
        更新机制：如果要选新leader了，就会term+1。
        条件：每个任期内只能有一个leader
        流程：
            一个term：[term+1 -> [选举time] -> [正常worktime]]
            整个系统：[term1 -一些s挂-> term2 -一些s挂-> term3 ->...]

    - 结构 关键的图二：
        —— server状态 ——
        server的稳定状态：
            currentTerm：
            votedFor：(follower)给candidatedId投票，如果没有就置-1
            log[]：存entry
        server的不稳定状态：
            commitIndex：已被大多节点保存的（已commit的）最高index。        
                Leader当前已经commit到了哪条index的LogEntry
                何时commit？当每次AppendEntries成功以后，检测某个index位置是否有多于一半的peers，如果是，标记为commit。注意，这里我们需要使用matchIndex来作为检测的手段，因为matchIndex代表着牢靠的，和Leader没有分歧的位置。
                * commitIndex是不只是Leader的概念，集群中所有节点的commitIndex应当保持一致。
            lastApplied：最后一个应用到状态机的log的index，初始为0。已applied的最高index。lastApplied和commitIndex类似，在标记完commitIndex以后，我们要做的就是通过applyCh同步apply消息，然后将lastApplied更新lastApplied。因此从某种程度来说，在我们Lab2中，两者几乎等效。在更新完commitIndex以后，通知applyCh，再更新lastApplied。
            lastApplied一定是小于等于commitIndex 的。
        leader的不稳定状态：
            matchIndex[]：
                leader用，用于辅助计算commitIndex。它记录的是确定的已和Leader同步的最高位置。（也可以用其它方式计算commitIndex）
                每个s都保存一份，它的index是每个s的id，长度是共有s的个数。那么matchIndex[serverX]就表示sX和leader同步的最高位置。
                如果本机就是leader，那么它一接收cmd就表示与自己同步了，就更新matchIndex为log的最后一格，（当前cmd就存在log的最后一格）。
                -> 就是说是已存完（不改）的位置
                这里设置Leader自己的matchIndex的目的主要是方便统计是否有多于一半的peers和Leader的log entry一致，然后进行commit（参考commitIndex）
                -> 就是说将要存的位置
                * prevLogIndex用nextindex-1来算，而不是matchindex来算
                * leader的logs不会跳，所以可以直接用长度计算
            nextIndex[]：
                leader用。和matchIndex结构一样。
                标记每个node缺失日志的开始位置【表现为即每个server的log长度】，对每个Follower发起AppendEntries时试replicated log就从这里（请注意，这里说的是尝试，这个值需要设置的比该follower对应的matchIndex大）。初始化为lastlogindex+1。
                比如说 s1为leader，nextIndex[] = {9,6},那么s2需要同步的log就是log[6] log[7] log[8]
            * nextIndex = matchIndex + 1

        —— RequestVote RPC —— 
        argument：
            term
            candidateId
            lastLogIndex
            lastLogTerm
        result：
            term：currentTerm
            voteGranted：bool，true表示candidate 接收了投票
        receiver implementation：
            if (args.)term < currentTerm:
                return False
            if voteFor == -1 or candidatedId and candidate.log is at least as up-to-date as receiver.log:
                vote
            如果两份日志最后的条目的任期号不同,那么任期号大的日志更加新;如果两份日志最后的条目任期号相同,那么日志比较长的那个就更加新

        —— AppendEntries RPC ——
        arguments：
            term: leader的term
            leaderId: 
            prevLogIndex: 新的日志条目紧随以前的索引值。
                用nextindex-1来算，而不是matchindex来算（why？）
            prevLogTerm: prevLogIndex 条目的任期号
            entries[]：存要复制给follower的entries。
                entries = copy(leader.logs[nextindex:])
            leaderCommit：leader的commitIndex
        result:
            term
            success
        receiver implementation:
            if term < currentTerm:
                return False
            if 若是日志在 prevLogIndex 位置处的日志条目的任期号和 prevLogTerm 不匹配：
                return False
                若是两个日志在相同的索引位置的日志条目的任期号相同，那么就认为这个日志从头到这个索引位置之间所有彻底相同
            if 一个已存的entry和新entry冲突：
                delete（existing entry）
                for follower followed existing entry：
                    delete（follower）
            if entry not in log: 就是说在👆都不冲突的条件下，如果缺log就开始复制entry
                log.append(entry)
            if leaderCommit > commitIndex:
                commitIndex = min(leaderCommit, index of last new entry)
            
        —— rules for servers ——
        all servers:
            if commitIndex > lastApplied:
                lastApplied + 1
                apply log[lastApplied] to state machine
            if RPC request or response contains term T > currentTerm:
                currentTerm = T
                send(currentTerm) to follower
        followers:
            response to RPCs from candidates and leaders
            if election timeout elapses without receiving AppendEntries RPC from current leader or granting vote to candidate:
                convert to candidate
        candidates:
            on conversion to candidate,开始选举：
                currentTerm+1
                vote for self
                reset election timer
                send requestvote rpcs to all other servers
            if votes > numofsesrvers//2 +1:
                become leader
            if appendEntries RPC received from new leader:
                convert to follower
            if election timeout elapses:
                开始选举
        leader：
            upon election：
                send initial empty appendentries RPCs(heartbeat) to each server
                repeat during idle periods to prevent election timeouts
                更新所有follower nextIndex=len(rf.logs) + 1
            if command received from client:
                log.append(entry)
                respond after entry applied to state machine
            if last log index >= nextIndex for a follower:
                send AppendEntries RPC with log entires staring at nextIndex
                if success:
                    说明已经成功在follower上replicated了log
                    更新nextIndex，更新matchIndex到最新位置
                    update nextIndex & matchIndex for follower
                if fail:
                    说明现在的nextIndex太大，nextIndex = nextIndex - 1再次尝试AppendEntries
            if N and N > commitIndex, a majority of matchIndex[i] >= N and log[N].term == currentTerm:
                commitIndex = N

A：leader选举

    实现选举单个领导者。
    发送接收选举req的rpc，选举的规则，leader的状态。

    go test -run 2A -race

    2A测试三个：
    ① TestInitialElection2A 测在正常运行状态下leader是否保持稳定
    ② TestReElection2A 测在有server挂时，是否可以正确选出leader
        - 旧leader加入不影响新leader
        - 达到法定人数，选出一个leader；少于法定人数，应该没有leader
    ③ TestManyElections2A 测多次选举是否可以正确选出leader
        开7个节点，随机挂3个，要求要么leader仍然在，要么在4个节点里选一个leader

    测试要求：
    - leader每秒发送心跳RPC不超过十次
    - 每个peer重置的timeout要不一样，以防无限loop选举
    - timeout要足够短。即使可能有多轮，选举都要5秒内结束（从上次旧leader失效开始算）。比paper里的150ms-300ms长

    测试给定server个数，通过cfg=make_config初始化整个系统。
        在make_config里遍历server个数，初始化每个server的log，并通过start1启动该server。
            start1:
                参数：
                    当前service的index 
                    applier是个函数，协程go发送，从apply ch拿message，看是否和log内容match
                在start1里用Make()初始化每个server：
                    Make的参数：
                        peers：服务器们
                        me：本serverid
                    除了给server赋值之外，每个server还会在这里起线程
                    线程ticker：轮询看本server有没有接不到消息，要不要选举。如果要，本节点就变成candid，并发投票。
                    线程applier

            所以是每一个server里都存了全套的logs、nextIndex和matchIndex


    选举的结构和流程：
    - 所有状态通过raft结构体管理，raft struct是一个整体的
    - 一变成该状态（调用transStatus）就开始这样做：leader负责发送心跳广播，follower负责选举计时、给别人投票，candidate负责发起投票。
    - 其中发送广播是一个不停循环的线程，那么就需要起一个go
    - 选举计时是每个server都hold有自己的计时器，所以是一个属性，一变成follower就reset
    - 发起投票是一个函数，cand遍历每个server，给每个follower都发送投票函数
    - 投票函数RequestVote：被要求投票的follower.requestVote(args:要求投它的cand, reply:要不要投)

    bug：
    · expected no leader among connected servers, but 0 claims to be leader:
    -> getState()的时候要加锁

    · 本来已经term4了，又回到term3且又两个leader:
    -> 如果本轮已经投过票了，就不应该再投了
    （leader的voteFor没有更新，一直是它自己，所以leader不会给别人投票的，只有变成follower的人才会更新voteFor为-1，巧妙）


B:数据同步

    实现Raft之间的日志复制。
    实现AppendEntries RPC以及发送AppendEntries RPC的方法HandleAppendEntries。

    go test -run 2A -race

    2B测试10个：
    ① TestBasicAgree2B
        - Start()运行之前，不能有提交的log存在
        - 
    ② TestRPCBytes2B
        基于RPC的字节数检查保证每个cmd都只对每个peer发送一次。
    ③ For2023TestFollowerFailure2B
    ④ For2023TestLeaderFailure2B
    ⑤ TestFailAgree2B
        断连小部分，不影响整体Raft集群的情况检测追加日志。
    ⑥ TestFailNoAgree2B
        断连过半数节点，保证无日志可以正常追加。然后又重新恢复节点，检测追加日志情况。
    ⑦ TestConcurrentStarts2B
        模拟客户端并发发送多个命令
    ⑧ TestRejoin2B
        Leader 1断连，再让旧leader 1接受日志，再给新Leader 2发送日志，2断连，再重连旧Leader 1，提交日志，再让2重连，再提交日志。
    ⑨ TestBackup2B
        先给Leader 1发送日志，然后断连3个Follower（总共1Ledaer 4Follower），网络分区。提交大量命令给1。然后让leader 1和其Follower下线，之前的3个Follower上线，向它们发送日志。然后在对剩下的仅有3个节点的Raft集群重复上面网络分区的过程。
    ⑩ TestCount2B
        检查无效的RPC个数，不能过多。


    注意：
    - 如果跑的太慢可能会挂
    - 在早期的 Lab 2B 测试中未能达成协议的一种方法是，即使领导者还活着，也要进行重复选举。?
    - 要在loop里加入time.Sleep(10 * time.Millisecond)，以免搞慢它以至于出bug


    日志同步要处理的结构和流程：
    
    - start() 如果是leader，就start leader。
        start时将（client）给的cmd传给leader，记在leader的log里。
        要返回这一轮儿从log的哪里（index）开始。这个开始的index，就是刚刚这个cmd存到的index。 就是当前log的最后一个，即len(logs) - 1
        同时呢，将这个index更新在matchIndex[本机id]上
        注意当转换状态至leader的时候，也要更新nextIndex和matchIndex

    - broadcastHeartbeat：
        只要当上了leader，就给每个人发广播。for peers/server遍历，go一个发送心跳的线程。
        对每个peer来说，会发送appendEntries来传递信息。
        需要传递的：
            本leader的term和id：看看此leader有无资格copy；
            prevlogindex和prevlogterm，就是本leader这里记录的要copy的follower的要记录的起点和term：看看能不能直接copy；
            本leader的commitid，看看能不能提交；
            整个logentries的副本。
        如果成功了（即不发送数据，或者数据全都成功copy了）：
            1.先更新nextIndex/matchIndex。nextIndex追加拷贝过去entries的个数 += len(args.LogEntries)；matchIndex为nextIndex - 1（或为之前matchIndex+ copy过entries的个数 args.prevLogIndex + len(args.LogEntries)）
            2.倒着遍历（只要i commit了，比i小的就都commit了不用再遍历）当前的logs index，到commitIndex为止：统计如果（len(peers)/2）的server都成功copy了，就把这个index的设成commit。
                如何统计呢，看matchindex。如果sX成功copy了第index个entry，就表示matchindex[sX]的值就应该>=index,(means 存了更多，那么index一定也存了)
        如果失败了：
            如果那个follower的term比本leader高，本leader就将为follower
            否则：
                本leader就把nextIndex退一位（失败是因为打算copy的位置太激进太新了），再试一次
                -> 优化：可以在同步的时候记录冲突的位置，这里就可以直接退到冲突的位置再试，不用一步一步退。

    - AppendEntries Rpc
        leader通过这个函数来传消息给follower。如果没数据同步，就是心跳包。如果有数据同步，Rpc中会携带需要同步的内容(LogEntry)
        同步的条件：
        1. 判断任期：
            如果当前（leader）发送来的term < 本机的term，就不处理
        2. 判断是否接收数据：success表示数据全接收，or本轮不同步数据
            因为会有bug的情况：当集群共同存（commit）了123后，分裂成两组，一组term1，更新的数据是5，即当前存的是1235；一组term2，更新了8，即当前存的数据是1238；但是term2不够>len/2+1,所以5一直是未提交。这时term1给term2的server传了这个8，term2的server就要回滚掉5，更新成1238。
            所以呢，如果要同步，需要这些信息：
                （每机都需要）存entry的地方：logs[LogEntry]
                需要被复制的entries：args.entries
                本机当前缺失的最早的index：len(rf.logs)
            * 更新的定义是，如果term更高就更新，如果term一样，就看谁的log更长就更新。
            
        3. 判断是否提交数据：
            如果本机的commitIndex小于leader的commitIndex，就可以更新commitIndex，设置commitIndex = min(leaderCommit, 最新entry在的index)，然后发送命令到ApplyCh，表示可以应用了。
            为甚么要min：
                因为有可能leader发给follower的max index也比当前leader的commitindex小，如果直接更新到commitindex，follower的log中间就有一些空挡。

    - commit提交。
        没提交意思就是client只给leader发了entries，leader还没给follower同步这些entries。
        提交了的意思就是，如果将来有什么bug需要回滚数据，也只能回滚到未提交的，已提交的数据就不能被重写了。
        commit的步骤：Log Replication
        1. leader把这些entries（副本）传给follower
        2. follower会返回给leader它有没有写好。
        3. 如果len/2+1的人都写好了，就算commit了。
        4. leader再发送给follower，通知该entries已commit
        5. 达成了共识（leader就可以return给client了）

        commit和apply：
        假如当前记录了commitIndex，但此时节点崩溃了，那么已提交的日志条目就并没有真正的应用到状态机中，所以需要applied这个属性记录真实应用到状态机的日志到哪里了。
        commitIndex表示最后一个状态为Commit的index；lastApplied表示最后一个状态为Applied的index
        - 一个entry被大多数server复制到了log
        - 指令是按顺序追加的，一个entry是commited，那么之前所有entry都是commited
        - 一旦follower确认一个entry是commited，他将在自己的状态机执行此entry
        - 由于只有Applied只能由Commit转换而来，所以lastApplied <= commitIndex一定成立
        只有当commit过的entries才会apply。当commitIndex大于lastApplied的时候，自增lastApplied,然后将日志应用到状态机上。执行：
            * 市面上有两种实现，一种是将apply放在主函数Make里面单开线程运行，这种用cond；一种是将apply放在setcommit函数里面，如果有commit的动作才开apply。

            这里用setcommit：记录上次appliedindex，+1为新apply开始的地方为startIdx；要apply的，到commite过的entry的位置位置，所以又记录entries=[lastapplied+1:commited+1],作为要apply的所有entry。
            然后将msg通过chan传给rf.appleCh。
            然后更新rf的lastApplied，为max(lastApplied, startIdx+要apply的个数)
            （？这里 有疑问为甚么要在循环里面更新lastApplied）

        
    - 修改RequestVote
        在labA里面，第一个来要票的人，follower就会投给它。这里需要update的是：如果有一个logs很短的server当了node，它要给别人同步的话，别的server里比它多的entry就都会丢失。
        所以投票时，该follower需要验证，leader的log长度不能短过自己，最后一个log的term【不能比本机小】，所以需要参数：
            leader的最后一个log的index：args.LastLogIndex <—— len(leader.logs) - 1
            leader的最后一个log的term：args.LastLogTerm <—— leader.logs[-1].Term
            本机的最后一个log的index：len(rf.logs) - 1
            本机的最后一个log的term：rf.logs[-1].Term



    注意的点：

    - 整个复制的过程，很重要的一点就是要保证算法的一致性。
    如何保证一致性：
    1. 数据不一致的判定标准：
        ① leader最多在一个term里在指定的一个日志索引位置建立一条日志条目，同时日志条目在日志中的位置也不会改变。
        ——>，那么，如果两个日志在相同索引的位置的Term相同，那么从开始到该索引位置的日志一定相同：
            当Leader.log[N-1].Term == Follower.log[N-1].Term时：就可以把Leader.log[N]同步到Follower，F.log[N] = L.log[N]

        ——> 那么就很好判定：只要 如果需要新加日志地方的上一条的term或index和leader记录的不一致，就表示不一致。
            需要同步日志序列log[x:]，并且要保证a的log[1:x]和自己的log[1:x]保持一致，关键点在于log[x-1]，即log[nextIndex[a]-1]。

            所以当appendEntry到serverX的时候，需要知道leader记录中，这个服务器的上条term/index, 还需要当前sX记录的自身的term/index
            leader的记录：
                args.prevLogIndex <—— leader.nextIndex[sX] - 1（N-1）
                args.prevLogTerm <—— leader.log[N-1].Term
            * 复制的entry从leader.nextIndex[sX]开始：rf.logs[rf.nextIndex[sX]:]
            rf自己的记录：
        * 如果sX的index过大，尚且再议，因为可能有暂存的entry，如果过小就铁不一致，因为leader太激进了会导致sX不连续，更早的本也应该copy进来，so直接返回；但是term只要不一样，就铁不一致。

        ② 领导人绝对不会删除或者覆盖本身的日志，只会增长。
        ——> which means，当数据不一致时，一切以leader数据为准。leader会强制复制本身的log到数据不一致的leader，从而使全部node都保持一致。
            ？如果新leader覆盖了旧leader的log呢？
            -> 会发生。但新任领导者必定会包含最新一条日志，即新任领导者的数据和旧领导者的数据就算是不一致的，也仅仅是未提交的日志，即客户端还没有获得回复，此时就算是新任领导者覆盖旧领导者的数据，客户端获得回复，持久化日志失败。从客户端的角度来开，并无产生数据不一致的状况。

    2. 日志被应用到各node有两个阶段（like 两阶段提交）：
        ① leader 带log发送appendEntries RPC
        ② leader判断log是否可被提交（len(peers)/2+1 都成功），如果是，则回复client；而后再并行的再发送带log的PRC，follower看到该log可被提交，则应用到各自的状态机中。
        暂时没成功的follower，leader会一直发RPC，直到所有node都一致。
        * 和两阶段提交的差异是：len(peers)/2+1 成功 vs 全node成功。

    3. 日志不一致时怎么办：
        附加log的PRC里，一致性检查失败时，follow会拒绝请求。leader发现请求失败后，会将附加log的index-1，再次尝试发送RPC，直至成功。
        * 小优化：follower拒绝之后，可以直接返回包含冲突的条目的任期号和本身存储的那个任期的最先的索引地址。减少通信次数。

    - 心跳时间和选举时间
        Leader在被选举出来后就会开始向其他Server发送心跳包，其他Server在收到心跳包后，就将自己的状态保持为Follower，直到下一次持续选举超时时间没有收到任何消息。
        which means：Leader发送心跳包存在着一个心跳包发送间隔，同时，Follower为竞选Leader也存在一个选举Timeout。所以，为了防止Leader已经选举出来，且网络没有Failure的情况下，一些Follower仍认为没有Leader存在的情况，选举Timeout应该是大于心跳间隔的。

    - Leader被选举出来后，Leader可以开始服务整个集群，也就要做到同步自身的数据给集群中的其他Server，因为，具有保存来自Client的数据的权力的只有Leader。其他Server如果收到Client的消息，会拒绝来自Client的消息，且可以选择返回Leader的位置。
 

    bug：
        · list超index：
        ——> appendentries的判断接收数据那里，两个条件要分开写。 

        · too many RPC bytes：
        ——> 注释太多

        · failed to reach agreement
        ——> 投票的时候，leader的lastlogterm比本机lastlogterm大也可以投票
        ——> 疯狂print，发现陷入死循环的地方所有人都变成candidate，但是拿不到别人的票。
            这里解决方法是把改状态的头一句，if 当前状态== 要改的状态：return，这句注释掉了。这样可以重新计时，增加term。在随机的影响下，会有人从candi里面挣脱出来。
            应该还有别的地方不规范，应该是在投票的时候，没有人给它投票的情况下怎么会死锁？？？


C：persistence

    将本来保存在内存的数据，适时保存入disk。
    同时优化AppendEntries RPC——加快日志回溯

    // 找到方便运行测试的脚本 
    bash go-test-many.sh 2000 8 2C 

    测试：
        TestPersist12C
        TestPersist22C(
        TestPersist32C
        TestFigure82C
        TestUnreliableAgree2C
        TestFigure8Unreliable2C
        TestReliableChurn2C
        TestUnreliableChurn2C

    持久化要处理的结构和流程：
    - persist() / readPersist()
        实现的时候并不会真的存在磁盘里，而是从 Persister 对象保存和恢复持久状态（in persister.go）。它保存了最近的持久化状态，raft也从这个节点恢复初始化。
        需要持久化的属性？
            currentTerm、votedFor、log
            (为甚么是这几个为甚么不是别的？)
        何时存储persist() ？
            每次状态改变的时候。
            即，选举的时候，降成follower的时候，初始化leader的时候，传递log的时候。
            * 传log的时候、投票的时候要defer；开始选举的时候、临时变follower的时候、leaderstart的时候不要defer（why）。投票完变leader不要persist（why）
            rf.persist()
        make时开启readPersist()

    - 优化AppendEntries RPC——加快日志回溯
        本来是：对于失败的AppendEntries请求，让nextIndex自减1
        优化为：如果找到一个冲突点，直接退回此冲突点
        需要属性两个：conflictterm 和 conflictindex
        优化1：
            如果follower.log不存在prevLog，让Leader下一次从follower.log的末尾开始同步日志。
        优化2：
            如果是因为prevLog.Term不匹配（而无法复制log），记follower.prevLog.Term为conflictTerm。
            ① 如果leader.log找不到Term为conflictTerm的日志，则下一次从follower.log中conflictTerm的第一个log的位置开始同步日志。
            ② 如果leader.log找到了Term为conflictTerm的日志，则下一次从leader.log中conflictTerm的最后一个log的下一个位置开始同步日志。
            * 注意是如果上一个（i-1）的term匹配该follower，next节点设为i