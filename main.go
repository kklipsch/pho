package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/html"

	"github.com/urfave/cli"
)

var recurseFlag = cli.BoolFlag{
	Name:  "recurse,r",
	Usage: "will recursively walk the gallery",
}

func main() {
	app := cli.NewApp()
	app.Name = "pho"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "url",
			EnvVar: "PHOTO_GALLERY_URL",
			Usage:  "base url to the photo gallery",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:   "ls",
			Usage:  "sr ls [path]",
			Action: ls,
			Flags:  []cli.Flag{recurseFlag},
		},
	}

	app.Run(os.Args)
}

func ls(ctx *cli.Context) {
	address := getAddress(ctx)
	recurse := ctx.Bool("recurse")

	var path string
	if len(ctx.Args()) > 0 {
		path = ctx.Args()[0]
	}

	fullPath := fmt.Sprintf("/var/albums/%s", path)
	err := walkPath(address, fullPath, recurse, "")
	if err != nil {
		log.Fatal(err)
	}

}

func walkPath(address string, path string, recurse bool, lead string) error {
	resp, err := http.Get(fmt.Sprintf("%s/%s", address, path))
	if err != nil {
		log.Fatal(err)
	}

	ct := getContentType(resp)
	switch ct {
	case "text/html":
		body := resp.Body
		defer body.Close()

		tokenizer := html.NewTokenizer(body)
		for {
			tt := tokenizer.Next()
			switch {
			case tt == html.ErrorToken:
				// End of the document, we're done
				return nil
			case tt == html.StartTagToken:
				t := tokenizer.Token()
				isAnchor := t.Data == "a"
				if !isAnchor {
					continue
				}

				ok, url := getHref(t)
				if !ok {
					return fmt.Errorf("No url for %v", t)
				}

				//probably the parent directory
				if strings.Contains(path, url) {
					continue
				}

				fmt.Printf("%s%v\n", lead, url)

				if recurse {
					err = walkPath(address, fmt.Sprintf("%v/%v", path, url), recurse, fmt.Sprintf("%s\t", lead))
					if err != nil {
						return err
					}
				}
			}
		}
	default:
	}

	return nil
}

func getContentType(resp *http.Response) string {
	parts := strings.Split(resp.Header.Get("Content-Type"), ";")
	return parts[0]
}

func getAddress(ctx *cli.Context) string {
	address := ctx.GlobalString("url")
	if address == "" {
		log.Fatal("host or must be provided")
	}

	return address
}

func getHref(t html.Token) (ok bool, href string) {
	for _, a := range t.Attr {
		if a.Key == "href" {
			href = a.Val
			ok = true
		}
	}

	return
}
