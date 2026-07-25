use crate::utils::add;
use crate::utils::{multiply, divide as div};
use crate::subpkg::helper::sub_fn;

pub mod utils;
pub mod subpkg;

fn local_fn() {}

fn main() {
    let _ = add(1, 2);
    let _ = multiply(2, 3);
    let _ = div(6, 2);
    sub_fn();
}
