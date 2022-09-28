1. 整个代码结构

    src 
    ├─mrapps  // 用来测试的不同功能的map和reduce函数
    │   ├─wc.go  // ① 测计数的正常函数
    │   ├─indexer.go  // ② 测不同输入的。计算文本出现在哪个文档的
    │   ├─mtiming.go // ③ 测map并行的
    │   ├─rtiming.go // ④ 测reduce并行的
    │   ├─jobcount.go // ⑤ 测试进程数量是否正确
    │   ├─early_exit.go // ⑥ 测试work是否有早退
    │   └─nocrash.go & crash.go // 测试容错性。一个提供正确答案，一个负责造成crash干扰。-
    ├─main  
    │   ├─mrsequential.go  //  串行调度mapreduce程序
    │   ├─mrmaster.go //  主master入口
    |   ├─mrworker.go // 主worker入口
    |   ├─pg*.txt  // 一些输入数据
    |   ├─...
    |   ├─test-mr.sh // 一个完整processes的测试
    |   ├─test-mr-many.sh // 测试n个test-mr，测超时
    ├─mr  // 需要编写的
        ├─master.go
        ├─worker.go
        └─rpc.go


2. 单线程的mapreduce：

    运行：
    go build -race -buildmode=plugin ../mrapps/wc.go
    go run -race mrsequential.go wc.so pg*.txt
    
    流程：

    src/wc.go 里有两个函数，map和reduce，负责统计单词和计算每个单词的总数
    将这两个函数打包（成wc.so），传递给mrsequential.go用
    在main/mrsequential.go中：
                other input：map           reduce
                               |             |
    input（**.txt）——>[{word, "1"}，...]        ——> "word count" \n
                                        |
                                      排序  
    map：
        input：arg0=文件名，arg1=文件内容（一整篇，当string）
        output：一个元素是hash的list [{word:"1"},...]，是所有txt里所有word的list
    reduce：
        input：reduce的input就是map的output
        output：直接写入文件，每个单词一行，每行like：word count，以字符串表现



3. 分布式架构：

    运行：
        go build -race -buildmode=plugin ../mrapps/wc.go
        // rm mr-out*
        go run -race mrcoordinator.go pg-*.txt // run一个
        go run -race mrworker.go wc.so // 另外窗口run好几个

    解释：

    一个coordinator和多个worker（实际应用中多个worker分布在不同机器上，这里再同一台机器）
    worker和coordinator通信用RPC
    worker问coordinator要task，每一个task中，input是*.txt，output是多个中间文件
    coordinator要看一个worker要是正进行task，就把task分给其他worker

    和单线程一样的是，
    mrsequential.go的输入是*.txt 和 wc.so，
    这里也要输入这两个，也要将wc.go的map和reduce打包成wc.so。

    区别是，
    这里，每个*.txt相当于一个被分割的输入，作为每一个map task的输入，就是说一个*.txt对应一个map task
    这里将所有*.txt作为main/mrcoordinator.go的输入，在它里面分配task by *.txt
    然后起若干个main/mrworker.go，它的输入是wc.so，（和上面也对应起来），
    当这些worker运行完之后，得到的output的总和应当与上面单线程的mr-out-0相同。

    根据test，需要完成五个地方：
    ① wc：计数
        go build -race -buildmode=plugin ../mrapps/wc.go
        计算的是单词在所有文本里面出现的个数

    ② indexer
        go build -race -buildmode=plugin ../mrapps/indexer.go
        计算的是单词出现在哪几个txt里。更改mapf和reducef的输入，看代码是否有普适性。如果没有实现多进程，输入就会有问题，这个就不过。

    ③ map parallelism & ④ reduce parallelism
        go build -race -buildmode=plugin ../mrapps/mtiming.go
        go build -race -buildmode=plugin ../mrapps/rtiming.go

        里面提供了一个并行文件，分别实施至map函数和reduce函数。
        worker在map/reduce阶段通过创建一个文件名为mr-worker+PID的文件，在map/reduce结束后删除该文件，通过统计存在几个特定文件名的数量，来判断同时运行的worker数量。
        表示一个worker只起一条进程，和一个文件对应。

    ⑤ job count
        go build -race -buildmode=plugin ../mrapps/jobcount.go
    
    ⑥ early exit
        go build -race -buildmode=plugin ../mrapps/early_exit.go
        检查 worker 是否会提前退出

    ⑦ crash 
        go build -race -buildmode=plugin ../mrapps/crash.go
        模拟进程挂掉/延迟
        crash.go里的干扰：给一个随机整数，如果小于某个值就推出这个进程。（所以测试的时候要起n个进程，测的就是多个进程时如果死了一个怎么办）
        

4. 规则和提示

    - map要把中间值分开放在bucket里给nReduce个reduce，（nReduce作为参数从main/mrcoordinator.go传递给MakeCoordinator()）
        所以每个mapper都需要创建nReduce个中间file给reduce tasks
    - 第x个reduce的oupout叫mr-out-X，一个mr-out-X格式和单线程一样:(%v %v).format(key, value)
    - 要做的地方在mr/worker.go, mr/coordinator.go, and mr/rpc.go
    - worker把map 的ouput放在当前路径，之后worker再把它们当成reduce的input
    - main/mrcoordinator.go 要mr/coordinator.go 提供一个 Done()方法，
        which 如果mr job完了就返回true，然后mrcoordinator.go就退出。
    - 如果job全完了，worker也要退出。通过call()来实现：
        如果workers和coordinator失联，就假设coordinator因为job完了而done，这个worker也就可以done
    - 如果修改了mr/的任何地方，就都要重build plugins
    - 开始：
        mr/worker.go的Worker() 传一个RPC给coordinator，要task；
        然后coordinator给这个worker返回一个文件名，并call map方法
    - 中间文件
        命名be like: mr-X-Y, X是map task的number，Y是reduce的
        可以存成json格式的，用encoding/json包
    - map part可以用 worker.go里的ihash(key)方法来选一个带key的reduce task
    - 读input，排序，output都可以用单线程的方法
    - coordinator 作为一个RPC server，是并发的，所以要lock for 安全
    - 如果worker慢了或者crash了，coordinator没办法，只能等一阵，然后重发。这个lab设定：
        coordinator等10s，然后就假设这个worker死了
    - 如果发生冲突，就可能发生中间命名也重复冲突了。可以用暂时名，完成了之后再改正式名
    - Go RPC
        发送的结构体和子结构体首字母必须大写
        当传reply结构体的指针给RPC时，*reply不能带任何字段


5. 流程：

    *.txt 作为输入，启动的是main/mrcoordinator.go
    main/mrcoordinator.go 是总控制，无具体实现，在里面调用mr/coordinator.go里的MakeCoordinator方法，给实例m。
    m还有方法done（也在mr/coordinator.go里），看task totally完了没。所以这个总控制就是调用具体的mr/coordinator，并且看它完了没。

    然后来到mr/coordinator，产生一个总的co实例c，该实例c有若干func。
    它的MakeCoordinator方法接受了从main/mrcoordinator.go传来的*.txt，每个file对应一个生成的task。
    它的ApplyForTask方法，是通过mr/worker.go里的Worker调用的（用RPC），给每个work分配一个task。

    那么先跳到mr/worker.go，Worker是由main/mrwor.go调用的具体worker的实现。
    Worker被传入了map和reduce的方法，加上从ApplyForTask那里得到的task信息，就可以具体做map和reduce的工作了。
    分别调用 doMap和doReduce 两个函数，实现计数。实现方法和单线程差不多。

        不同的地方就是每个task都生成各自的中间file，存到本地，lab建议用json存，文件名格式是mr-X-Y。X是map的num，Y是reduce的num。

        中间可能创建完文件但是fail了，所以建议先建一个mr-tmp-X-Y的文件，确定搞好了再转成mr-X-Y

        最终输出文件是mr-out-Y。

        X是map的个数，即一开始task的个数，即file的个数，所以这个值一开始遍历files的时候就定好了是遍历的index。
        Y是reduce的个数，因为它用hash对word取余而分reduce，就是说相同的word一定在y序号相同的文件里，所以最终合并y相同的文件。

    当当前task计好数、每个临时文件都生成好了之后，选择交由coordinator改名、从队列里删除，彻底完结。
    所以建立一个改名、删除的func，由worker端交过来已写好的taskid触发。写好一个就处理一个。

    如果所有都pop出队列了，需要一个func判断是否可以trans到下个阶段。

    另外有一个独立的func一直运行，来判断现在队列里的task有没有超时的，超时就重新放入队列。

    整个流程就是这样。


6. 结构体
    coordinator结构体表示整个work的内容，所以存的是一些全局的变量，比如整个file（map）有多少个，需要分成多少个reduce。还需要存储当前阶段所有的task内容，存成一个map，完成了一个就pop它。
    由于这个工作的特点是，所有map完成了之后才能开始reduce，所以用全局变量标注整个project当前的阶段，map还是reduce还是done。

    一个coord里面有n个task，这些task都处于同一个阶段，但是有可能是不同worker处理的task。

    task表示每一个任务。有可能表示map任务，负责将content处理成key-value（或别种统计），也有可能是reduce任务，负责将key-value处理成key-count（或别种统计）。所以它需要记的参数有：开始时间，taskindex，workerid，filename。在task阶段，taskindex和filename一一对应。在reduce阶段，taskindex就按reducenum逐个算。wrokerid是当某一个worker来要task的时候由这个worker带来，分配时赋值。开始时间也在这时分配时赋值，表示处理开始。

    woker是每一个开启的进程，在终端run一个Worker，就表示start起一个worker，算一个workerid。如果开了好几个go run worker.go，就有好几个workerid。每个task可能由不同的worker负责，所以workerid随task标记，方便track。

    此外由于workerid是在请求任务时才赋值的，所以它还可以表示当前task有没有在work的状态，如果已经有id了却超时，可以重新赋值-1，表示还没开始，并丢回队列里重新开始。

    req-arg和req-reply是请求rpc时需要的结构体，reply负责将coord端的task信息带到worker端处理，刚开始直接赋值整个task，后来发现给个别值就行了。arg刚开始没有用，后来发现可以带返上一个完成task的信息给coord，方便处理file的改名、删除等等。

7. 调试：
    错误出现在，定义c的map[]task时，以为map的元素更新值了之后map里的就自动改了，后来发现不是。所以map改成存储指针。

8. todo
    单机版workerid，如果真分布式要改生成规则
    filename、排序都在内存，如果过大可能会爆
    如果真分布，rpc如何通信
    中间结果数据的传输？有两类方案：
        - 直接写入到如 AWS S3 等共享存储。改动成本低，但依赖外部服务
        - 参考 Google MapReduce 的做法，保存在 Map Worker 的本地磁盘，Reduce Worker 通过 RPC 向 Map Worker 拉取数据
    coord太重了，单点。