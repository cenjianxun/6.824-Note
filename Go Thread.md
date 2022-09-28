1. 闭包 closures & Goroutine
    ```
    package main

    import "sync"

    func main(){
        var wg sync.WaitGroup
        for i := 0, i < 5; i ++ {
            wg.Add(i)
            // 使用goroutine，定义匿名函数
            go func(x int) { 
                sendRPC(x)
                wg.Done() // 匿名函数可以修改包外变量
            }(i) // 意思是将i当作匿名函数的参数传给x
        }
        wg.Wait()
    }

    func sendRPC(i int){
        println(i) // 这里打印出来的是随机排列的数字0-4
        // 如果上面的匿名函数不传参数like：
        // go func() {sendRPC(i)}()
        // 就会打印45555
        // 因为当运行sendRPC时，for循环已经改变了i的值
    }
    ```
    方便发送rpc。
    当需要candidates投票选leader是，需要从所有follower那里投票，而不是one by one。
    rpc是阻塞操作。
    or
    leader想给所有follower发送追加内容，也适合用并行而不是串行。


    // ————————————
    想启动一个raft，定期发送heartbreak，然后raft调用.kill, 就要杀死所有gorounine
    ```
    import "time"
    import "sync"

    var done bool
    var mu sync.Mutex

    func main(){
        time.Sleep(1 * time.Second)
        println("started")
        go periodic() // 无限循环的goroutine
        time.Sleep(5 * time.Second) // 等待一段时间就把全局变量done设为true
        // 因为done为共享变量，会被多个线程修改。所以改时要加锁。下锁同。
        mu.Lock() 
        done = true
        mu.Unlock()
        println("cancelled")
        time.Sleep(3 * time.Second)
    }

    func periodic() {
        for {
            println("tick")
            time.Sleep(1 * time.Second)
            mu.Lock() // 为了保证可以观察到这个done已经写成true了
            if done { // 如果done为true就结束这个goroutine
                return
            }
            mu.Unlock()
        }
    }
    ```
    * 如果a已经用锁了，b要是再要锁，b就会阻塞，直到a释放锁
    * 如果main函数（线程）结束了（线程资源被释放），它启动的所有goroutine（协程，用的是线程资源）也都会结束


    ```
    func main(){
        // 银行业务 俩人各1万
        alice := 10000
        bob := 10000
        var mu sync.Mutex
        total := alice + bob

        // 转钱
        go func() {
            for i := 0; i < 1000; i++ {
                // 如果这里不加锁，x个协程运行，可能会破坏其他协程，使最终结果不为x
                mu.Lock()
                defer mu.Unlock()
                // 如果这里解锁又加锁，就无法保证原子性。这里sum需要是不变量，锁需要可以保护不变量。
                alice -= 1
                bob += 1
            }
        }()
        go func() {
            for i := 0; i < 1000; i++ {
                mu.Lock()
                defer mu.Unlock()
                alice += 1
                bob -= 1
            }
        }()

        start := time.Now()
        for time.Since(start) < 1 * time.Second {
            mu.Lock()
            if alice + bob != total {
                fmt.Printf("observed viloation, alice = %v, bob = %v, sum = %v\n, alice, bob, alice + bob)
            }
            mu.Unlock()
        }
    }
    ```


2. condition variable 条件变量
    raft peer变成一个candidate, 想给所有follower发投票请求，follower返回信息给candidate，表示它有无投票。
    这里询问应该用并行而不是串行，而且不用问完，只要超过一半票数就可以停了。

    cond：通过内再变化改变外部的条件判断。
    cond.signle提高效率尽量不用
    ```
    func main(){
        rand.Seed(time.Now().UnixNano())
        count := 0
        finished := 0 //得到响应的数量
        var mu sync.Mutex
        // 解决busy waiting的好方法：cond
        cond := sync.NewCond(&mu) // 1. 锁的指针传给cond

        for i := 0; i < 10; i++{
            go func(){
                vote := requestVote()
                mu.Lock() // 2.加锁
                if vote {
                    count++
                }
                finished++
                // 必须在有锁的条件下才能调用cond. 
                cond.Broadcast() // 3.它唤醒在cond.wait()处等待的线程
                mu.Unlock() // 4.然后释放锁
            }()
        }

        mu.Lock()

        //for { 
        // !! busy waiting 反复检查一个条件，反复抢锁cpu被榨干
        //    mu.Lock()
        //    if count < 5 && finished != 10 {
        //        break
        //    }
        //    mu.Unlock()
        //    // 解决busy waiting的一个方法:但是50是一个定值
        //    // time.Sleep(50 * time.Millisecond)
        //}

        
        for count < 5 && finished != 10 { // 1. 检查条件 4.有锁时回到条件检查
            // 2.如果条件为false，且有锁-> 3
            cond.Wait() // 3.调用wait,以原子方式释放锁，并将锁放入等待线程列表
        }           
        // 接4 所以这里是有锁的

        if count >=  5 {
            println("received 5+ vote!")
        } else {
            println("lost")
        }
        mu.Unlock()
    }
    ```

3. channel
    对数量小的场景有用
    (unbuffer的)：
    channel没有内部存储空间
    channel是同步的，如果有人发了但没人接，就会阻塞
    channel不能用于单个goroutine上，必须有另一个goroutine同一时刻做相反操作

    buffer的：
    channel在没填满时是异步，填满了就和unbuffer一样
    ```
    func main() {
        c := make(chan int)
        for i := 0; i < 4; i++ {
            go doWork(c)
        }
        for {
            v := <-c
            println(v)
        }
    }

    func doWork(c chan int) {
        for {
            time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
            c <- rand.Int()
        }
    }

    // 相当于：
    func main() {
        var wg sync.WaitGroup
        for i := 0; i < 5; i++ {
            wg.Add(1) // 对内部counter +1
            // 在go的外部调用wg，先执行add(1)再执行go
            // 所以done永远在add之后调用
            go func(x int) {
                sendRPC(x)
                wg.Done()
            }(i)
        }
        wg.Wait() // 调用wait，它会等到直到done被调用的次数与add被调用的次数相同
    }

    func sendRPC(i int) {
        println(i)
    }
    ```