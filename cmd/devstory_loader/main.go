package main

import (
	"fmt"
	"os"
	"hally/internal/app"
	"hally/internal/machine"
)

func main() {
	path := "/home/cloud-user/code/hally-devstory/testdata/apps/dev-story/app.yaml"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	def, err := app.Load(path)
	if err != nil {
		fmt.Printf("LOAD ERROR:\n%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("App loaded: %s v%s\n", def.App.ID, def.App.Version)
	
	_, err = machine.New(def)
	if err != nil {
		fmt.Printf("MACHINE ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Machine created OK")
}
