mod collision;
mod ffi;
mod greedy;
mod light;
mod quad;
mod raycast;
mod step;
// Task 3 先冻结解析视图，Task 4/5 才由算法消费全部访问器。
#[allow(dead_code)]
mod input;
