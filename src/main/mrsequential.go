package main

//
// simple sequential MapReduce.
//
// go run mrsequential.go wc.so pg*.txt
//

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"plugin"
	"sort"

	"6.824/mr"
)

// for sorting by key.
type ByKey []mr.KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

func main() {
	// os.Args 是input的参数，共有三个
	// arg0：mrsequential.go
	// arg1: wc.go
	// arg2: pg*.txt 多个输入文件算一个参数
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: mrsequential xxx.so inputfiles...\n")
		os.Exit(1)
	}

	// --------------------------- map任务开始 --------------------------
	// 从arg1（wc.so）中读取map和reduce
	// wc.so 相当于凝成的各个func，这里mapf和reducef就是调用那两个func
	mapf, reducef := loadPlugin(os.Args[1])
	//fmt.Printf(mapf)
	//
	// read each input file,
	// pass it to Map,
	// accumulate the intermediate Map output.
	//
	intermediate := []mr.KeyValue{}
	for _, filename := range os.Args[2:] {
		file, err := os.Open(filename)
		if err != nil {
			log.Fatalf("cannot open %v", filename)
		}
		content, err := ioutil.ReadAll(file)
		if err != nil {
			log.Fatalf("cannot read %v", filename)
		}
		file.Close()

		// 产生中间结果
		// kva 是list，list的每个元素是一个{key value},like:[{Project 1} {Gutenberg 1} ...]
		// intermediate是更大的list，将kva们append在一起
		kva := mapf(filename, string(content))
		intermediate = append(intermediate, kva...)
	}
	// --------------------------- map任务结束 --------------------------

	//
	// a big difference from real MapReduce is that all the
	// intermediate data is in one place, intermediate[],
	// rather than being partitioned into NxM buckets.
	//

	// 这里将中间结果排序是使相同word排在一起，方便累计。
	sort.Sort(ByKey(intermediate))

	// 创建输出文件
	oname := "mr-out"
	ofile, _ := os.Create(oname)

	// --------------------------- reduce任务开始 --------------------------
	//
	// call Reduce on each distinct key in intermediate[],
	// and print the result to mr-out-0.
	//
	i := 0
	for i < len(intermediate) {
		// 这里i和j最后表示为同一个单词的第一个和最后一个
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		// fmt.Println(intermediate[i : j+1])
		// 这里intermediate里的各元素还没合并，每个key的值都是1，like:
		// [{youths 1} {youths 1} {yow 1}], [{yow 1} {yow 1} {yow 1} {yow 1} {yuther 1}]
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}

		output := reducef(intermediate[i].Key, values)
		//fmt.Println(intermediate[i], values)
		//fmt.Println(intermediate[i].Key, output)
		// key是该单词，values仍然没合并，是k个1之list，k是该单词的次数
		// reducef才计算count，且用的不是加法，用的是value的长度

		// this is the correct format for each line of Reduce output.
		fmt.Fprintf(ofile, "%v %v\n", intermediate[i].Key, output)

		i = j
	}
	// --------------------------- reduce任务结束 --------------------------
	ofile.Close()
}

// 加载插件中的方法，输入是插件文件名，返回值是map和reduce方法
//
// load the application Map and Reduce functions
// from a plugin file, e.g. ../mrapps/wc.so
//
func loadPlugin(filename string) (func(string, string) []mr.KeyValue, func(string, []string) string) {
	p, err := plugin.Open(filename)
	if err != nil {
		log.Fatalf("cannot load plugin %v", filename)
	}
	// 查找插件中Map方法，并赋给mapf
	xmapf, err := p.Lookup("Map")
	if err != nil {
		log.Fatalf("cannot find Map in %v", filename)
	}
	mapf := xmapf.(func(string, string) []mr.KeyValue)

	// 查找插件中的Reduce方法并赋给reducef
	xreducef, err := p.Lookup("Reduce")
	if err != nil {
		log.Fatalf("cannot find Reduce in %v", filename)
	}
	reducef := xreducef.(func(string, []string) string)

	return mapf, reducef
}
