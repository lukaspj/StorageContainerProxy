package main

import (
	"log"
)

func main() {
	err := GetRootCmd().Execute()
	if err != nil {
		log.Fatal(err)
	}
}
