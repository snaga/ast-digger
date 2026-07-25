package references

func UseValue() {
	message := "TargetFunc is not a reference here"
	value := TargetFunc
	_ = message
	_ = value
}
