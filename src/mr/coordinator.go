package mr

import (
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"time"
)

const (
	MAP    = "MAP"
	REDUCE = "REDUCE"
	DONE   = "DONE"
)

type Coordinator struct {
	// Your definitions here.
	lock     sync.Mutex
	phase    string
	nMap     int
	nReduce  int
	taskChan chan string
	tasks    map[string]*Task
}

type Task struct {
	WorkerID int
	//	Status    TaskType
	Index     int
	InputFile string
	StartTime time.Time
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	// for test slice part files:
	//files = files[:2]

	c := Coordinator{
		phase:    MAP,
		nMap:     len(files),
		nReduce:  nReduce,
		tasks:    make(map[string]*Task),
		taskChan: make(chan string, int(math.Max(float64(len(files)), float64(nReduce)))),
	}

	log.Printf("---- Coordinator Start ----\n")
	c.InitMapTask(files)
	go c.schedule()
	c.server()

	return &c
}

func (c *Coordinator) InitMapTask(files []string) {
	for i, filename := range files {
		task := Task{
			WorkerID:  -1,
			Index:     i,
			InputFile: filename,
			StartTime: time.Time{},
		}
		taskId := getTaskID(c.phase, task.Index)
		c.tasks[taskId] = &task
		c.taskChan <- taskId
		//fmt.Printf("coord get file %s\n", filename)
	}

}

func getTaskID(t string, index int) string {
	return fmt.Sprintf("%s-%d", t, index)
}

func (c *Coordinator) InitReduceTask() {
	for i := 0; i < c.nReduce; i++ {
		task := Task{
			WorkerID: -1,
			Index:    i,
			//	InputFile: MROutFile("mr", i),
			StartTime: time.Time{},
		}
		taskId := getTaskID(c.phase, task.Index)
		c.tasks[taskId] = &task
		c.taskChan <- taskId
	}
	//fmt.Printf("reduce get tasks\n")
}

// Your code here -- RPC handlers for the worker to call.

func (c *Coordinator) ApplyForTask(args *ReqTaskArgs, reply *ReqTaskReply) error {
	if args.LastTaskIndex != -1 {
		c.finishTask(args)
	}

	taskId, ok := <-c.taskChan
	if !ok {
		//c.transPhase()
		return nil
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	log.Printf("assign Task %s-%d to Worker %d", c.phase, c.tasks[taskId].Index, args.WorkerID)
	c.tasks[taskId].WorkerID = args.WorkerID
	c.tasks[taskId].StartTime = time.Now()
	reply.WorkPhase = c.phase
	reply.NMap = c.nMap
	reply.NReduce = c.nReduce
	reply.InputFile = c.tasks[taskId].InputFile
	reply.TaskIndex = c.tasks[taskId].Index
	fmt.Printf("now task: %v in tasks: \n %v \n", c.tasks[taskId], checkMap(c.tasks))
	return nil
}

func (c *Coordinator) finishTask(args *ReqTaskArgs) {
	c.lock.Lock()
	defer c.lock.Unlock()
	taskId := getTaskID(args.LastTaskPhase, args.LastTaskIndex)
	if task, ok := c.tasks[taskId]; ok {
		fmt.Printf("this task %s in %d: %v\n", taskId, task.WorkerID, checkMap(c.tasks))
		log.Printf("Worker %d finished Task %s-%d ", args.WorkerID, args.LastTaskPhase, args.LastTaskIndex)
		switch args.LastTaskPhase {
		case MAP:
			for i := 0; i < c.nReduce; i++ {
				tmpMapFile := MRFileName("mr-inter-tmp", args.LastTaskIndex, i)
				mapFile := MRFileName("mr-inter", args.LastTaskIndex, i)
				err := os.Rename(tmpMapFile, mapFile)
				if err != nil {
					log.Fatalf("map output %s failed: %e", tmpMapFile, err)
				}
				//log.Printf("so file mr-inter-%d-%d saved.", args.LastTaskIndex, i)
			}
		case REDUCE:
			tmpReduceFile := MRFileName("mr-out-tmp", args.LastTaskIndex)
			ReduceFile := MRFileName("mr-out", args.LastTaskIndex)
			err := os.Rename(tmpReduceFile, ReduceFile)
			if err != nil {
				log.Fatalf("reduce output %s failed: %e", tmpReduceFile, err)
			}
			//log.Printf("so file mr-out-%d saved.", args.LastTaskIndex)
		}
		delete(c.tasks, taskId)
		log.Printf("now %s in worker %d saved&deleted, left tasks:%v", taskId, args.WorkerID, checkMap(c.tasks))
		if len(c.tasks) == 0 {
			c.transPhase()
		}
	}
}

func (c *Coordinator) transPhase() {
	// 是否要再判断一下list里是否有？
	//log.Printf("trans phase, 睇睇task: %d", len(c.tasks))
	switch c.phase {
	case MAP:
		//log.Printf("finish all MAP!")
		c.phase = REDUCE
		//c.tasks = make(map[string]*Task)
		c.InitReduceTask()
	case REDUCE:
		log.Printf("finish all REDUCE!")
		close(c.taskChan)
		c.phase = DONE
	default:
		log.Printf("chan empty and let me see phash%s……", c.phase)
	}
}

func checkMap(tasks map[string]*Task) []string {
	var show []string
	for tid, task := range tasks {
		show = append(show, MRFileName(task.WorkerID, tid))
	}
	return show
}

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

func (c *Coordinator) schedule() {
	for {
		time.Sleep(1 * time.Second)
		c.lock.Lock()
		for tid, task := range c.tasks {
			//fmt.Printf("巡逻: task %d %s, running %s\n", task.WorkerID, tid, task.StartTime.Format("2006-01-02 15:04:05"))
			if task.WorkerID != -1 && time.Since(task.StartTime).Seconds() > 10 {
				log.Printf("worker %d 's task %s timeout, redo", task.WorkerID, tid)
				task.WorkerID = -1
				c.taskChan <- tid
			}
		}
		c.lock.Unlock()
	}
}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	c.lock.Lock()
	ret := c.phase == DONE
	c.lock.Unlock()
	return ret
}
