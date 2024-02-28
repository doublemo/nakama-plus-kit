package nakamapluskit

import (
	"fmt"
	"testing"
)

func TestServiceRegistry(t *testing.T) {
	m := map[string]int{"A": 1, "B": 2, "C": 3, "D": 4, "E": 5}
	v := map[string]int{"F": 6}

	o := m
	m = v
	fmt.Println(o, v)
}
