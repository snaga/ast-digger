// helperFn is called below (comment reference, must not count)
pub fn use_helper() -> &'static str {
    helperFn() // actual reference on this line
}
