use std::sync::mpsc;
use std::time::Duration;

#[inline(always)]
pub fn get2(s: &[u8]) -> u16 {
    s[0] as u16 | (s[1] as u16) << 8
}

#[inline(always)]
pub fn place2(s: &mut [u8], u: u16) {
    s[0] = u as u8;
    s[1] = (u >> 8) as u8;
}

#[inline(always)]
pub fn append2(s: &mut impl Extend<u8>, u: u16) {
    s.extend(u.to_le_bytes());
}

#[inline(always)]
pub fn get8(s: &[u8]) -> u64 {
    s[0] as u64
        | (s[1] as u64) << 8
        | (s[2] as u64) << 16
        | (s[3] as u64) << 24
        | (s[4] as u64) << 32
        | (s[5] as u64) << 40
        | (s[6] as u64) << 48
        | (s[7] as u64) << 56
}

#[inline(always)]
pub fn place8(s: &mut [u8], u: u64) {
    s[0] = u as u8;
    s[1] = (u >> 8) as u8;
    s[2] = (u >> 16) as u8;
    s[3] = (u >> 24) as u8;
    s[4] = (u >> 32) as u8;
    s[5] = (u >> 40) as u8;
    s[6] = (u >> 48) as u8;
    s[7] = (u >> 56) as u8;
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
        Self {
            values_tx,
            values_rx,
            new_fn,
        }
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_get2() {
        let u = 0x516fu16;
        let bytes = u.to_le_bytes();
        assert_eq!(u, get2(&bytes));

        let mut v = Vec::new();
        append2(&mut v, u);
        assert_eq!(v, bytes);

        let mut v = vec![0u8; 2];
        place2(&mut v, u);
        assert_eq!(v, bytes);
    }

    #[test]
    fn test_get8() {
        let u = 0x516f5ad3u64;
        let bytes = u.to_le_bytes();
        assert_eq!(u, get8(&bytes));

        let mut v = Vec::new();
        append8(&mut v, u);
        assert_eq!(v, bytes);

        let mut v = vec![0u8; 8];
        place8(&mut v, u);
        assert_eq!(v, bytes);
    }
}
