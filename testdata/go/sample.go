package sample

import "fmt"

// TopLevelFunction doubles x.
func TopLevelFunction(x int) int {
	return x * 2
}

// Greeter is a struct responsible for greetings.
type Greeter struct {
	Prefix string
}

// Greet returns a greeting string using a pointer receiver.
func (g *Greeter) Greet(name string) string {
	return fmt.Sprintf("%s, %s!", g.Prefix, name)
}

// Shout returns an uppercased message using a value receiver.
func (g Greeter) Shout(message string) string {
	return message
}

// Animal describes something that can speak.
type Animal interface {
	// Speak returns the sound the animal makes.
	Speak() string
}

// ID is a type alias for an identifier.
type ID = string

// Count is a named type based on int.
type Count int

func NoDocFunction() {
}
