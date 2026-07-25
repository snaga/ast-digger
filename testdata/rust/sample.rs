//! Inner doc comment for module

/// Outer doc comment for helper function
fn top_level_function(x: i32) -> i32 {
    x * 2
}

/// Greeter struct definition
pub struct Greeter {
    pub name: String,
}

/// Direction enum
pub enum Direction {
    Up,
    Down,
}

/// Speak trait
pub trait Speak {
    fn speak(&self) -> String;
}

/// Mod item
pub mod utils {
    pub fn helper() {}
}

impl Greeter {
    /// New greeter constructor
    pub fn new(name: &str) -> Self {
        Greeter {
            name: name.to_string(),
        }
    }

    /// Greet method
    pub fn greet(&self) -> String {
        format!("Hello, {}!", self.name)
    }
}

impl Speak for Greeter {
    fn speak(&self) -> String {
        self.greet()
    }
}
