package main

import (
	"io"
	"log"
	"net"
	"os"

	"crypto/md5"
	"encoding/gob"
	"io/ioutil"
)

type state struct {
	nextSeq       int
	latestFileSeq map[string]int

	fileName  string
	inputFile *os.File
	tempFile  *os.File
}

type fileToSync struct {
	name  string
	seqNo int
}

func serverNetworkReader(conn *net.Conn, c chan *ClientMsg) {
	dec := gob.NewDecoder(*conn)

	for {
		msg := ClientMsg{}

		err := dec.Decode(&msg)
		fatalIfErr(err)

		c <- &msg
	}
}

func serverNetworkWriter(conn *net.Conn, c chan *ServerMsg) {
	enc := gob.NewEncoder(*conn)

	for {
		msg := <-c

		err := enc.Encode(msg)
		fatalIfErr(err)
	}
}

func startNextFileReception(dir string, state *state, msg *ClientMsg) {
	state.fileName = msg.File

	file, err := os.Open(dir + "/" + state.fileName)
	if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	state.inputFile = file

	file, err = ioutil.TempFile(dir, ".tmp.")
	fatalIfErr(err)
	state.tempFile = file

	log.Println("Processing new file:", msg.Op, "file:", msg.File)
}

func processUpdateMsg(dir string, state *state, msg *ClientMsg) {
	log.Println("Got message type:", msg.Op, "file:", msg.File)

	if state.fileName == "" {
		startNextFileReception(dir, state, msg)
	}

	switch msg.Op {

	case FileReuseData:
		if state.inputFile != nil {
			buf := make([]byte, msg.Length)

			n, err := state.inputFile.Read(buf)
			if err == nil {
				log.Println("Reusing:", n, "bytes of file:", msg.File)
				state.tempFile.Write(buf[0:n])
			} else if err != io.EOF {
				log.Fatal(err)
			}
		}

	case FileNewData:
		if state.inputFile != nil {
			state.inputFile.Seek(int64(msg.Length), 1)
		}

		log.Println("Received raw data for file:", msg.File, "len:", len(msg.Data))
		state.tempFile.Write(msg.Data)

	case FileEnd:
		log.Println("Finishing processing of file:", msg.File)

		if state.inputFile != nil {
			state.inputFile.Close()
			state.inputFile = nil
		}

		path := state.tempFile.Name()

		state.tempFile.Close()
		state.tempFile = nil

		seqNo, ok := state.latestFileSeq[state.fileName]
		if ok && seqNo == msg.SeqNo {
			err := os.Rename(path, dir+"/"+state.fileName)
			fatalIfErr(err)
		} else {
			err := os.Remove(path)
			fatalIfErr(err)
		}

		state.fileName = ""
	}
}

func serverHashSender(dir string, outChan chan *ServerMsg, fileChan chan fileToSync) {

	for {
		sync := <-fileChan

		file, err := os.Open(dir + "/" + sync.name)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Fatal(err)
			}

			msg := ServerMsg{File: sync.name, SeqNo: sync.seqNo, Length: 0, Last: true}
			outChan <- &msg

			log.Println("Sending request for new file: ", sync.name)

			continue
		}

		for {
			buf := make([]byte, 4096)
			n, err := file.Read(buf)
			if err != nil {
				if err == io.EOF {
					break
				} else {
					log.Fatal(err)
				}
			}

			log.Println("Sending hash of chunk of file:", sync.name, "chunk length:", n)

			msg := ServerMsg{File: sync.name, SeqNo: sync.seqNo, Length: n}
			msg.Hash = md5.Sum(buf[0:n])

			outChan <- &msg
		}

		msg := ServerMsg{File: sync.name, SeqNo: sync.seqNo, Length: 0, Last: true}
		outChan <- &msg

		file.Close()
	}
}

func serverLoop(dir string, inChan chan *ClientMsg, fileChan chan fileToSync) {
	state := state{}
	state.latestFileSeq = map[string]int{}

	for {
		msg := <-inChan

		switch msg.Op {

		case FileUpdated:
			state.nextSeq++
			state.latestFileSeq[msg.File] = state.nextSeq
			fileChan <- fileToSync{name: msg.File, seqNo: state.nextSeq}
			log.Println("File: ", msg.File, " updated")

		case FileRemoved:
			delete(state.latestFileSeq, msg.File)

			err := os.Remove(dir + "/" + msg.File)
			if err != nil && !os.IsNotExist(err) {
				log.Fatal(err)
			}
			log.Println("File: ", msg.File, " removed")

		case FileNewData:
			fallthrough
		case FileReuseData:
			fallthrough
		case FileEnd:
			processUpdateMsg(dir, &state, msg)
		}
	}
}

func server(dir string, port string) {
	ln, err := net.Listen("tcp", ":"+port)
	fatalIfErr(err)

	conn, err := ln.Accept()
	fatalIfErr(err)

	log.Println("Got connection, starting")

	inChan := make(chan *ClientMsg)
	outChan := make(chan *ServerMsg)
	fileChan := make(chan fileToSync)

	go serverNetworkReader(&conn, inChan)
	go serverNetworkWriter(&conn, outChan)
	go serverHashSender(dir, outChan, fileChan)

	serverLoop(dir, inChan, fileChan)
}
