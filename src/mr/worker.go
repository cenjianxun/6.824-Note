package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"log"
	"net/rpc"
	"os"
	"sort"
	"strconv"
	"strings"
)

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}

type ByKey []KeyValue

func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

type worker struct {
	workerID string
	mapF     func(string, string) []KeyValue
	reduceF  func(string, []string) string
}

//
// main/mrworker.go calls this function.
//

func getWorkerID() int {
	return os.Getpid()
}

func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {
	workerId := getWorkerID()
	log.Printf("-- worker %d starts --\n", workerId)
	lastTaskIndex := -1
	lastTaskPhase := ""
	for {
		args := ReqTaskArgs{
			WorkerID:      workerId,
			LastTaskIndex: lastTaskIndex,
			LastTaskPhase: lastTaskPhase,
		}
		reply := ReqTaskReply{}
		if ok := call("Coordinator.ApplyForTask", &args, &reply); !ok {
			log.Fatal("no task request.")
			break
		}

		switch reply.WorkPhase {
		case MAP:
			doMap(mapf, &reply)
		case REDUCE:
			doReduce(reducef, &reply)
			// case WAIT:
			// 	time.Sleep(1 * time.Second)
		}
		lastTaskIndex = reply.TaskIndex
		lastTaskPhase = reply.WorkPhase
		//log.Printf("Task %s %d finished counting", reply.WorkPhase, reply.TaskIndex)
	}

	log.Printf("-- Worker %d finished --\n", workerId)

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()

}

func doMap(mapf func(string, string) []KeyValue, reply *ReqTaskReply) {
	content := getFileContent(reply.InputFile)
	kva := mapf(reply.InputFile, string(content))
	hashedKV := make([][]KeyValue, reply.NReduce)
	for _, kv := range kva {
		hashed := ihash(kv.Key) % reply.NReduce
		hashedKV[hashed] = append(hashedKV[hashed], kv)
	}
	for i := 0; i < reply.NReduce; i++ {
		ofile, err := os.Create(MRFileName("mr-inter-tmp", reply.TaskIndex, i))
		if err != nil {
			panic(err.Error())
		}
		enc := json.NewEncoder(ofile)
		for _, kv := range hashedKV[i] {
			enc.Encode(kv)
		}
		ofile.Close()
		fmt.Printf("saved mr-inter-tmp-%d-%d \r", reply.TaskIndex, i)
	}

}

func doReduce(reducef func(string, []string) string, reply *ReqTaskReply) {
	var kva []KeyValue
	for i := 0; i < reply.NMap; i++ {
		inputFile := MRFileName("mr-inter", i, reply.TaskIndex)
		ifile, err := os.Open(inputFile)
		if err != nil {
			log.Fatal("Unable to read from:", inputFile)
		}
		dec := json.NewDecoder(ifile)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kva = append(kva, kv)
		}
		ifile.Close()
	}
	sort.Sort(ByKey(kva))
	ofile, err := os.Create(MRFileName("mr-out-tmp", reply.TaskIndex))
	if err != nil {
		panic(err.Error())
	}
	for i := 0; i < len(kva); {
		j := i + 1
		for j < len(kva) && kva[j].Key == kva[i].Key {
			j++
		}
		var values []string
		for k := i; k < j; k++ {
			values = append(values, kva[k].Value)
		}
		output := reducef(kva[i].Key, values)
		fmt.Fprintf(ofile, "%v %v\n", kva[i].Key, output)
		i = j
	}
	ofile.Close()
	fmt.Printf("saved mr-out-tmp-%d \n", reply.TaskIndex)
}

func getFileContent(filename string) []byte {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open %v", filename)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", filename)
	}
	file.Close()
	return content
}

func MRFileName(args ...interface{}) string {
	var name []string
	for _, arg := range args {
		switch arg.(type) {
		case int:
			name = append(name, strconv.Itoa(arg.(int)))
		case string:
			name = append(name, arg.(string))
		}
	}
	return fmt.Sprintf(strings.Join(name, "-"))
}

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
