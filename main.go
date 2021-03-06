package main

import (
	"bytes"
	"crypto/sha1"
	"flag"
	"fmt"
	"github.com/anderejd/syncext"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type resultSlice []result

func (r resultSlice) Len() int {
	return len(r)
}

func (r resultSlice) Less(i, j int) bool {
	a := &r[i]
	b := &r[j]
	c := bytes.Compare(a.Sum, b.Sum)
	if -1 == c {
		return true
	}
	if 1 == c {
		return false
	}
	c = strings.Compare(a.Path, b.Path)
	if -1 == c {
		return true
	}
	return false
}

func (r resultSlice) Swap(i, j int) {
	tmp := r[i]
	r[i] = r[j]
	r[j] = tmp
}

func printResultBuffer(basepath string, rs resultSlice) error {
	var dupBytes int64
	var totBytes int64
	var dups int
	var sum []byte
	for _, r := range rs {
		p, err := filepath.Rel(basepath, r.Path)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "%x\t%s\n", r.Sum, p)
		totBytes += r.Size
		if !bytes.Equal(r.Sum, sum) {
			sum = r.Sum
			continue
		}
		dups++
		dupBytes += r.Size
	}
	dupMB := float64(dupBytes) / 1024 / 1024
	totMB := float64(totBytes) / 1024 / 1024
	log("Duplicates   :", dups)
	log("Duplicate MB :", dupMB)
	log("Total MB     :", totMB)
	return nil
}

func log(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
}

func logStatus(MBps float64, files int, MBpsTotal float64) {
	const format = "MB/s: %.2f\tfiles: %d\tMB/s (total): %.2f\n"
	fmt.Fprintf(os.Stderr, format, MBps, files, MBpsTotal)
}

func calcSha1(path string) (sum []byte, written int64, err error) {
	var f *os.File
	f, err = os.Open(path)
	if nil != err {
		return
	}
	defer f.Close()
	h := sha1.New()
	written, err = io.Copy(h, f)
	if nil != err {
		return
	}
	sum = h.Sum(nil)
	return
}

// Result struct for a single file.
// Err will be nil on success.
type result struct {
	Path string
	Sum  []byte
	Size int64
	Err  error
}

// The returned result channel will close when done.
func produceConcurrent(dirpath string) <-chan result {
	res := make(chan result)
	jobs := make(chan string)
	work := func() {
		for path := range jobs {
			sum, size, err := calcSha1(path)
			res <- result{path, sum, size, err}
		}
	}
	syncext.FanOut(runtime.NumCPU(), work, func() { close(res) })
	go produceJobs(dirpath, jobs, res)
	return res
}

func produceJobs(dirpath string, jobs chan<- string, res chan<- result) {
	err := processDir(dirpath, jobs)
	if err != nil {
		res <- result{"", nil, 0, err}
	}
	close(jobs)
}

func processDir(path string, jobs chan<- string) error {
	if isDotPath(path) {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	list, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return err
	}
	for _, f := range list {
		p := filepath.Join(path, f.Name())
		if !f.IsDir() {
			if f.Mode().IsRegular() {
				jobs <- p
			}
		} else {
			err = processDir(p, jobs)
			if nil != err {
				return err
			}
		}
	}
	return nil
}

func isDotPath(p string) bool {
	b := filepath.Base(p)
	if ".." != b && len(b) > 1 && '.' == b[0] {
		return true
	}
	return false
}

func processRootDir(dirpath string) error {
	res := produceConcurrent(dirpath)
	ta := time.Now()
	files := 0
	i := 0
	var MBpsTotal float64
	var bytes int64
	resBuff := make(resultSlice, 0)
	for r := range res {
		bytes += r.Size
		files++
		if r.Err != nil {
			return r.Err
		}
		tb := time.Now()
		s := tb.Sub(ta).Seconds()
		if s > 1.0 {
			i++
			bytesPerSec := float64(bytes) / s
			MBps := bytesPerSec / 1024 / 1024
			MBpsTotal += (MBps - MBpsTotal) / float64(i)
			logStatus(MBps, files, MBpsTotal)
			ta = tb
			bytes = 0
			files = 0
		}
		resBuff = append(resBuff, r)
	}
	sort.Sort(resBuff)
	return printResultBuffer(dirpath, resBuff)
}

func main() {
	flag.Parse()
	dirpath := flag.Arg(0)
	if "" == dirpath {
		log("ERROR: Arg 0 (dirpath) missing.")
		os.Exit(1)
	}
	err := processRootDir(dirpath)
	if err != nil {
		log("ERROR: ", err)
		os.Exit(1)
	}
}
