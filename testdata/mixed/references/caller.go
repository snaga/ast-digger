package mixedrefs

// helperFn is called below (comment reference, must not count)
func useHelper() string {
	return helperFn() // actual reference on this line
}
