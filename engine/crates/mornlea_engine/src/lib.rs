mod collision;
mod ffi;
mod greedy;
mod light;
// 任务 2.1 才接入 FFI 出口;此前先以模块级实现与单测消费(同 input 冻结先例)。
#[allow(dead_code)]
mod lod;
mod quad;
mod raycast;
mod step;
mod worldgen;
// Task 3 先冻结解析视图，Task 4/5 才由算法消费全部访问器。
#[allow(dead_code)]
mod input;
