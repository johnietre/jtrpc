use std::sync::mpsc;
use std::time::Duration;

// NOTE: Dereferencing the pointers can cause panic on misaligned pointer dereference

#[cfg(target_endian = "little")]
#[inline(always)]
pub fn get2(s: &[u8]) -> u16 {
    assert!(s.len() >= 2, "length must be at least 2");
    cfg_if::cfg_if! {
        if #[cfg(target_endian = "little")] {
            unsafe { *s.as_ptr().cast::<u16>() }
        } else {
            unsafe { (*s.as_ptr().cast::<u16>()).to_be() }
        }
    }
}

#[inline(always)]
pub fn place2(s: &mut [u8], u: u16) {
    assert!(s.len() >= 2, "length must be at least 2");
    unsafe { *s.as_mut_ptr().cast::<u16>() = u.to_le(); }
}

#[inline(always)]
pub fn append2(s: &mut impl Extend<u8>, u: u16) {
    s.extend(u.to_le_bytes());
}

#[cfg(target_endian = "little")]
#[inline(always)]
pub fn get8(s: &[u8]) -> u64 {
    assert!(s.len() >= 8, "length must be at least 8");
    cfg_if::cfg_if! {
        if #[cfg(target_endian = "little")] {
            unsafe { *s.as_ptr().cast::<u64>() }
        } else {
            unsafe { (*s.as_ptr().cast::<u64>()).to_be() }
        }
    }
}

#[inline(always)]
pub fn place8(s: &mut [u8], u: u64) {
    assert!(s.len() >= 8, "length must be at least 8");
    unsafe { *s.as_mut_ptr().cast::<u64>() = u.to_le(); }
}

#[inline(always)]
pub fn append8(s: &mut impl Extend<u8>, u: u64) {
    s.extend(u.to_le_bytes());
}

pub struct Pool<T> {
    values_tx: mpsc::Sender<T>,
    values_rx: mpsc::Receiver<T>,
    new_fn: Option<Box<dyn Fn() -> T>>,
}

impl<T> Pool<T> {
    // Creates a new pool with an optional function to call when creating new values.
    pub fn new(new_fn: Option<Box<dyn Fn() -> T>>) -> Self {
        let (values_tx, values_rx) = mpsc::channel();
        Self { values_tx, values_rx, new_fn }
    }

    /// Attempts to get a value from the pool. If there are no values, and true is passed, a new
    /// value is created and returned, returning Some((T, true)). If there is a value (nothing was
    /// created), Some((T, false)) is returned. Otherwise, if no new value is to be created and
    /// there were no values, None is returned.
    pub fn get(&self, new: bool) -> Option<(T, bool)> {
        if let Ok(v) = self.values_rx.try_recv() {
            return Some((v, false));
        } else if !new {
            return None;
        }
        self.new_fn.as_ref().map(|f| (f(), true))
    }

    /// Attempts to get a value from the pool within the specified timeout (or indefinitely if
    /// there is no timeout). Has the same return structure as `get`.
    pub fn get_timeout(&self, new: bool, timeout: Option<Duration>) -> Option<(T, bool)> {
        let v = if let Some(timeout) = timeout {
            self.values_rx.recv_timeout(timeout).ok()
        } else {
            Some(self.values_rx.recv().unwrap())
        };
        if let Some(v) = v {
            return Some((v, false));
        } else if !new {
            return None;
        }
        self.new_fn.as_ref().map(|f| (f(), true))
    }

    /// Adds a value to the pool.
    pub fn put(&self, v: T) {
        self.values_tx.send(v).unwrap();
    }

    pub fn new_fn(&self) -> Option<&dyn Fn() -> T> {
        self.new_fn.as_ref().map(|f| &**f)
    }
}
