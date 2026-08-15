mod collision;
mod ffi;
mod greedy;
mod light;
mod quad;
mod raycast;
mod step;
// Task 2.2 由 ffi 导出消费;在此之前允许未使用。
#[allow(dead_code)]
mod worldgen;
// Task 3 先冻结解析视图，Task 4/5 才由算法消费全部访问器。
#[allow(dead_code)]
mod input;
