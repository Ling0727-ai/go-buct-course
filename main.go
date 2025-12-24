package main

import "github.com/Ling0727-ai/go-buct-course/client"

func main() {
	// 对应 Python: client = BUCTClient()
	// 对应 Python: client.run_interactive()
	c := client.New("", "")
	c.RunInteractive()
}
