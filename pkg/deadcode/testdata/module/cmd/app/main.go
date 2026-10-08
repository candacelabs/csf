package main

import "example.com/fixture/lib"

func main() {
	lib.Used()
}

// deadInMain is never called; nothing outside main can call it.
func deadInMain() {}
