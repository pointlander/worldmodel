// Copyright 2026 The WorldModel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"

	"github.com/pointlander/worldmodel/iris"
)

func main() {
	original, err := os.ReadFile("iris/iris.data")
	if err != nil {
		panic(err)
	}
	m, err := iris.Search(original)
	if err != nil {
		panic(err)
	}
	output, err := iris.Run(m.Prog)
	if err != nil {
		panic(fmt.Errorf("short: %v", err))
	}
	for i, v := range original {
		if output[i] != v {
			panic("not the same")
		}
	}
	fmt.Println(len(original), len(m.Prog))
}
