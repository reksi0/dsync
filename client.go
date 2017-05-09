package main

import (
	"io"
	"log"
	"net"
	"os"

	"crypto/md5"
	"encoding/gob"
	"io/ioutil"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func isFile(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeType) == 0
}

func watcher(dir string, ch chan fsnotify.Event) {
	watcher, err := fsnotify.NewWatcher()
	fatalIfErr(err)

	err = watcher.Add(dir)
	fatalIfErr(err)

	files, err := ioutil.ReadDir(dir)
	fatalIfErr(err)

	for _, file := range files {
		log.Println("Adding file:", file.Name())
		path := dir + "/" + file.Name()
		if isFile(path) {
			ch <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		}
	}

	for {
		select {
		case event := <-watcher.Events:
			log.Println("Watch, file :", event.Name, " op:", event.Op)

			if event.Op&fsnotify.Remove != 0 ||
				(event.Op&(fsnotify.Create|fsnotify.Write) != 0 &&
					isFile(event.Name)) {
				ch <- event
			}

		case err := <-watcher.Errors:
			log.Fatal("Watch error:", err)
		}
	}
}

func clientNetworkReader(conn *net.Conn, ch chan *ServerMsg) {
	dec := gob.NewDecoder(*conn)
	for {
		msg := new(ServerMsg)

		err := dec.Decode(msg)
		fatalIfErr(err)

		ch <- msg
	}
}

func clientNetworkWriter(conn *net.Conn, ch chan *ClientMsg) {
	enc := gob.NewEncoder(*conn)

	for {
		msg := <-ch
		err := enc.Encode(msg)
		fatalIfErr(err)
	}
}

type FileSyncState struct {
	name  string
	file  *os.File
	seqNo int
}

func startNextFileProcessing(dir string, state *FileSyncState, msg *ServerMsg) {
	state.seqNo = msg.SeqNo
	state.name = msg.File

	if state.file != nil {
		state.file.Close()
		state.file = nil
	}

	file, err := os.Open(dir + "/" + msg.File)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}

	state.file = file
}

func finishFileProcessing(state *FileSyncState, outChan chan *ClientMsg, msg *ServerMsg) {
	for {
		buf := make([]byte, 4096)

		n, err := state.file.Read(buf)
		if err != nil {
			if err == io.EOF {
				omsg := ClientMsg{SeqNo: state.seqNo, Op: FileEnd, File: msg.File}
				outChan <- &omsg
				return
			} else {
				log.Fatal(err)
			}
		}

		log.Println("Sending new chunk for file (new):", msg.File, "len:", n)
		omsg := ClientMsg{SeqNo: state.seqNo, Length: n, Op: FileNewData, Data: buf[0:n], File: msg.File}
		outChan <- &omsg
	}

}

func processChunk(state *FileSyncState, outChan chan *ClientMsg, msg *ServerMsg) {
	buf := make([]byte, msg.Length)

	n, err := state.file.Read(buf)
	if err != nil && err != io.EOF {
		log.Fatal(err)
	}

	if n != 0 {
		omsg := ClientMsg{SeqNo: state.seqNo, Length: msg.Length, File: msg.File}

		if n != msg.Length || md5.Sum(buf[0:n]) != msg.Hash {
			omsg.Op = FileNewData
			omsg.Data = buf[0:n]
			log.Println("Sending new chunk for file:", msg.File, "len:", n)
		} else {
			omsg.Op = FileReuseData
			log.Println("Reusing chunk of file:", msg.File, "len:", n)
		}

		outChan <- &omsg
	}
}

func processServerMsg(dir string, state *FileSyncState, outChan chan *ClientMsg, msg *ServerMsg) {
	if state.name != msg.File || state.seqNo != msg.SeqNo {
		startNextFileProcessing(dir, state, msg)
	}

	if state.file != nil {
		processChunk(state, outChan, msg)

		if msg.Last {
			finishFileProcessing(state, outChan, msg)
		}
	}
}

func clientLoop(dir string, fileChan chan fsnotify.Event, inChan chan *ServerMsg, outChan chan *ClientMsg) {
	state := FileSyncState{}

	for {
		select {
		case event := <-fileChan:
			path, err := filepath.Rel(dir, event.Name)
			fatalIfErr(err)

			msg := ClientMsg{File: path}
			if event.Op == fsnotify.Remove {
				msg.Op = FileRemoved
			} else {
				msg.Op = FileUpdated
			}

			outChan <- &msg

		case msg := <-inChan:
			processServerMsg(dir, &state, outChan, msg)
		}
	}

}

func client(dir string, addr string) {
	log.Println("Starting client, connecting to: ", addr)

	conn, err := net.Dial("tcp", addr)
	fatalIfErr(err)

	log.Println("Connected to: ", addr)

	fileChan := make(chan fsnotify.Event)
	go watcher(dir, fileChan)

	inChan := make(chan *ServerMsg)
	go clientNetworkReader(&conn, inChan)

	outChan := make(chan *ClientMsg)
	go clientNetworkWriter(&conn, outChan)

	clientLoop(dir, fileChan, inChan, outChan)
}
