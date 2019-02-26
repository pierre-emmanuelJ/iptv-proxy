# M3U

[![CircleCI](https://img.shields.io/circleci/project/github/jamesnetherton/m3u/master.svg)](https://circleci.com/gh/jamesnetherton/m3u/tree/master)
[![license](https://img.shields.io/github/license/mashape/apistatus.svg?maxAge=600)](https://opensource.org/licenses/MIT)

A basic golang [M3U playlist](https://en.wikipedia.org/wiki/M3U) parser library.

## Installation

```
go get github.com/jamesnetherton/m3u
```

## Usage

Example using a local playlist:

```go
package main

import (
	"fmt"
	"github.com/jamesnetherton/m3u"
)

func main() {
	playlist, err := m3u.Parse("testdata/playlist.m3u")

	if err == nil {
		for _, track := range playlist.Tracks {
			fmt.Println("Track name:", track.Name)
			fmt.Println("Track length:", track.Length)
			fmt.Println("Track URI:", track.URI)
			fmt.Println("Track Tags:")
			for i := range track.Tags {
				fmt.Println(" -",track.Tags[i].Name,"=>",track.Tags[i].Value)

			}
			fmt.Println("----------")
		}	
	} else {
		fmt.Println(err)
	}
}
```

Example using a remote playlist:

```go
package main

import (
	"fmt"
	"github.com/jamesnetherton/m3u"
)

func main() {
	playlist, err := m3u.Parse("https://raw.githubusercontent.com/jamesnetherton/m3u/master/testdata/playlist.m3u")

	if err == nil {
		for _, track := range playlist.Tracks {
			fmt.Println("Track name:", track.Name)
			fmt.Println("Track length:", track.Length)
			fmt.Println("Track URI:", track.URI)
			fmt.Println("Track Tags:")
			for i := range track.Tags {
				fmt.Println(" -",track.Tags[i].Name,"=>",track.Tags[i].Value)

			}
			fmt.Println("----------")
		}	
	} else {
		fmt.Println(err)
	}
}
```
