package main

import "log"
import "runtime/debug"

func fatalIfErr(err error) {
	if err != nil {
		debug.PrintStack()
		log.Fatal(err)
	}
}
