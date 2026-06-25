package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func main() {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			fmt.Printf("BROWSER CONSOLE: ")
			for _, arg := range ev.Args {
				fmt.Printf("%s ", arg.Value)
			}
			fmt.Println()
		}
	})

	url := "http://127.0.0.1:8080/profiles.php?scenario=success&XID=1234567#autorevive=10&cbport=33601"
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Sleep(2*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
}
