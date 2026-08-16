//! 显存区间分配器:Go `internal/render/pool.go` 的逐语义移植。
//!
//! best-fit 选块、Free 时与相邻空闲块合并;单位是"面数"而非字节。
//! 行为必须与 Go 版一致——section 在 faces 缓冲中的偏移由它决定,
//! 偏移不同不影响图像,但保持一致便于对拍与排查。

/// 池中的一段分配,单位为面数。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Alloc {
    pub offset: u32,
    pub size: u32,
}

#[derive(Clone, Copy)]
struct FreeBlock {
    offset: u32,
    size: u32,
}

/// best-fit、可合并相邻空闲块的区间分配器。
pub struct Pool {
    capacity: u32,
    free: Vec<FreeBlock>,
    used: u32,
}

impl Pool {
    /// 新建容量为 `capacity` 个面的池。
    pub fn new(capacity: u32) -> Self {
        Self {
            capacity,
            free: vec![FreeBlock {
                offset: 0,
                size: capacity,
            }],
            used: 0,
        }
    }

    /// 分配 `faces` 个面;空间不足返回 None。
    pub fn alloc(&mut self, faces: u32) -> Option<Alloc> {
        if faces == 0 || faces > self.capacity {
            return None;
        }
        let mut best: Option<usize> = None;
        for (i, b) in self.free.iter().enumerate() {
            if b.size < faces {
                continue;
            }
            if best.is_none_or(|j| b.size < self.free[j].size) {
                best = Some(i);
            }
        }
        let best = best?;
        let block = self.free[best];
        let alloc = Alloc {
            offset: block.offset,
            size: faces,
        };
        if block.size == faces {
            self.free.remove(best);
        } else {
            self.free[best] = FreeBlock {
                offset: block.offset + faces,
                size: block.size - faces,
            };
        }
        self.used += faces;
        Some(alloc)
    }

    /// 归还一段空间,并与相邻空闲块合并。
    pub fn free(&mut self, alloc: Alloc) {
        if alloc.size == 0 {
            return;
        }
        self.used -= alloc.size;
        let i = self.free.partition_point(|b| b.offset < alloc.offset);
        self.free.insert(
            i,
            FreeBlock {
                offset: alloc.offset,
                size: alloc.size,
            },
        );
        if i + 1 < self.free.len()
            && self.free[i].offset + self.free[i].size == self.free[i + 1].offset
        {
            self.free[i].size += self.free[i + 1].size;
            self.free.remove(i + 1);
        }
        if i > 0 && self.free[i - 1].offset + self.free[i - 1].size == self.free[i].offset {
            self.free[i - 1].size += self.free[i].size;
            self.free.remove(i);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn best_fit_alloc_and_merge_on_free() {
        let mut pool = Pool::new(100);
        let a = pool.alloc(30).unwrap();
        let b = pool.alloc(30).unwrap();
        let c = pool.alloc(40).unwrap();
        assert_eq!((a.offset, b.offset, c.offset), (0, 30, 60));
        assert!(pool.alloc(1).is_none());

        // 释放中段后 best-fit 复用小洞。
        pool.free(b);
        let d = pool.alloc(10).unwrap();
        assert_eq!(d.offset, 30);

        // 相邻释放必须合并,合并后能分配大块。
        pool.free(a);
        pool.free(d);
        pool.free(c);
        let e = pool.alloc(100).unwrap();
        assert_eq!(e.offset, 0);
    }
}
