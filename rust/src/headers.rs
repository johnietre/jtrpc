use crate::{utils::*, BV};
use std::collections::HashMap;
use std::mem;

pub const MAX_HEADERS_LEN: usize = (1 << 16) - 1;

type HM = HashMap<BV, BV>;

#[derive(Default, Clone, Debug)]
pub struct Headers(InnerHeaders);

impl Headers {
    pub(crate) fn from_bytes(b: BV) -> Self {
        Self(InnerHeaders::Bytes(b))
    }

    pub fn is_parsed(&self) -> bool {
        self.0.is_parsed()
    }

    pub fn parse(&mut self) {
        self.0.parse()
    }

    pub fn encode(&mut self) -> bool {
        self.0.encode()
    }

    pub fn encodep(&mut self) -> Option<HM> {
        self.0.encodep()
    }

    pub fn encoded_len(&self) -> usize {
        self.0.encoded_len()
    }

    pub fn get(&self, key: &[u8]) -> Option<&[u8]> {
        self.0.get(key)
    }

    pub fn set(&mut self, key: BV, value: BV) -> bool {
        self.0.set(key, value)
    }

    pub fn set_all(&mut self, m: HM) -> Result<(), HM> {
        self.0.set_all(m)
    }

    pub fn remove(&mut self, key: &[u8]) -> Option<BV> {
        self.0.remove(key)
    }

    pub fn map(&self) -> Option<&HM> {
        self.0.map()
    }

    pub fn bytes(&self) -> Option<&[u8]> {
        self.0.bytes()
    }

    pub fn try_into_map(self) -> Result<HM, Self> {
        self.0.try_into_map().map_err(Self)
    }

    pub fn try_into_bytes(self) -> Result<BV, Self> {
        self.0.try_into_bytes().map_err(Self)
    }

    pub fn iter<'a>(&'a self) -> Iter<'a> {
        match &self.0 {
            InnerHeaders::Map(m) => Iter(IterEnum::Map(m.iter())),
            InnerHeaders::Bytes(b) => Iter(IterEnum::Bytes(b.as_slice())),
        }
    }
}

#[derive(Clone, Debug)]
enum InnerHeaders {
    Bytes(BV),
    Map(HashMap<BV, BV>),
}

impl InnerHeaders {
    fn is_parsed(&self) -> bool {
        matches!(self, Self::Map(_))
    }

    fn parse(&mut self) {
        use InnerHeaders::*;
        let b = match self {
            Map(_) => return,
            Bytes(b) => b,
        };
        let mut m = HashMap::new();
        let mut l = b.len();
        while l >= 4 {
            let (kl, vl) = (get2(&b) as usize, get2(&b[2..]) as usize);
            let kvl = kl + vl;
            if kvl > l {
                break;
            }
            l -= 4;
            let mut value = b.split_off(kl);
            let key = mem::replace(b, value.split_off(vl));
            m.insert(key, value);
            l -= kvl;
        }
        *self = Map(m);
    }

    /// Returns false if the encoded length would exceed MAX_HEADERS_LEN.
    fn encode(&mut self) -> bool {
        use InnerHeaders::*;
        let m = match self {
            Bytes(_) => return true,
            Map(m) => m,
        };
        let (mut b, mut kv, mut headers_len) = (Vec::new(), None, 0);
        let mut iter = m.drain();
        while let Some((mut k, mut v)) = iter.next() {
            let (kl, vl) = (k.len(), v.len());
            headers_len += kl + vl;
            if headers_len > MAX_HEADERS_LEN {
                kv = Some((k, v));
                break;
            }
            append2(&mut b, kl as u16);
            append2(&mut b, vl as u16);
            b.append(&mut k);
            b.append(&mut v);
        }
        if kv.is_none() {
            // Satify borrow checker's "m is still used here (`iter` drop)" or something
            mem::drop(iter);
            *self = Bytes(b);
            return true;
        }
        *self = Map(iter.chain(Headers(Bytes(b))).collect::<HM>());
        false
    }

    /// Returns the encoded bytes and the headers that couldn't fit, if there were any.
    fn encodep(&mut self) -> Option<HM> {
        use InnerHeaders::*;
        let m = match self {
            Bytes(_) => return None,
            Map(m) => m,
        };
        let (mut b, mut rem, mut headers_len) = (Vec::new(), HM::new(), 0);
        for (mut k, mut v) in m.drain() {
            let (kl, vl) = (k.len(), v.len());
            let kvl = kl + vl;
            headers_len += kvl;
            if headers_len > MAX_HEADERS_LEN {
                rem.insert(k, v);
                headers_len -= kvl;
                continue;
            }
            append2(&mut b, kl as u16);
            append2(&mut b, vl as u16);
            b.append(&mut k);
            b.append(&mut v);
        }
        *self = Bytes(b);
        (rem.len() == 0).then_some(rem)
    }

    fn encoded_len(&self) -> usize {
        match self {
            InnerHeaders::Bytes(b) => b.len(),
            InnerHeaders::Map(m) => m.iter().fold(0, |acc, (k, v)| acc + 4 + k.len() + v.len()),
        }
    }

    fn get(&self, key: &[u8]) -> Option<&[u8]> {
        let mut b = match self {
            InnerHeaders::Map(m) => return m.get(key).map(|v| v.as_slice()),
            InnerHeaders::Bytes(b) => b.as_slice(),
        };
        while b.len() >= 4 {
            let (kl, vl) = (get2(b) as usize, get2(&b[2..]) as usize);
            let kvl = kl + vl;
            b = &b[4..];
            if b.get(..kl)? == key {
                return b.get(kl..kvl);
            }
            b = &b[kvl..];
        }
        None
    }

    fn set(&mut self, mut key: BV, mut value: BV) -> bool {
        let b = match self {
            InnerHeaders::Map(m) => {
                m.insert(key, value);
                return true;
            }
            InnerHeaders::Bytes(b) => b,
        };
        let (kl, vl) = (key.len(), value.len());
        let total = 4 + kl + vl;
        if total + b.len() > MAX_HEADERS_LEN {
            return false;
        }
        b.reserve(total);
        append2(b, kl as u16);
        append2(b, vl as u16);
        b.append(&mut key);
        b.append(&mut value);
        true
    }

    fn set_all(&mut self, m: HM) -> Result<(), HM> {
        let h = InnerHeaders::Map(m);
        if self.is_parsed() {
            *self = h;
            return Ok(());
        }
        todo!()
    }

    fn remove(&mut self, key: &[u8]) -> Option<Vec<u8>> {
        let bm = match self {
            InnerHeaders::Map(m) => return m.remove(key),
            InnerHeaders::Bytes(b) => b,
        };
        let mut b = bm.as_slice();
        let (mut i, mut _l) = (0, b.len());
        while b.len() >= 4 {
            let (kl, vl) = (get2(b) as usize, get2(&b[2..]) as usize);
            let kvl = kl + vl;
            b = &b[4..];
            if b.get(..kl)? == key {
                let mut to_rm = bm.split_off(i);
                let mut rest = to_rm.split_off(4 + kvl);
                bm.append(&mut rest);
                return Some(to_rm.split_off(4 + kl));
            }
            b = &b[kvl..];
            i += 4 + kvl;
        }
        None
    }

    fn map(&self) -> Option<&HM> {
        match self {
            InnerHeaders::Map(m) => Some(m),
            InnerHeaders::Bytes(_) => None,
        }
    }

    fn bytes(&self) -> Option<&[u8]> {
        match self {
            InnerHeaders::Bytes(b) => Some(b),
            InnerHeaders::Map(_) => None,
        }
    }

    fn try_into_map(self) -> Result<HM, Self> {
        match self {
            InnerHeaders::Map(m) => Ok(m),
            InnerHeaders::Bytes(_) => Err(self),
        }
    }

    fn try_into_bytes(self) -> Result<BV, Self> {
        match self {
            InnerHeaders::Bytes(b) => Ok(b),
            InnerHeaders::Map(_) => Err(self),
        }
    }
}

impl Default for InnerHeaders {
    fn default() -> Self {
        InnerHeaders::Bytes(Vec::new())
    }
}

type IterItem<'a> = (&'a [u8], &'a [u8]);

pub struct Iter<'a>(IterEnum<'a>);

impl<'a> Iterator for Iter<'a> {
    type Item = IterItem<'a>;

    fn next(&mut self) -> Option<Self::Item> {
        use IterEnum::*;
        match self.0 {
            Map(ref mut iter) => iter.next().map(|(k, v)| (k.as_slice(), v.as_slice())),
            Bytes(mut b) => {
                if b.len() < 4 {
                    return None;
                }
                let (kl, vl) = (get2(b) as usize, get2(&b[2..]) as usize);
                let kvl = kl + vl;
                b = &b[4..];
                if kvl > b.len() {
                    self.0 = Bytes(&b[b.len()..]);
                    return None;
                }
                let kv = (&b[..kl], &b[kl..kvl]);
                self.0 = Bytes(&b[kvl..]);
                Some(kv)
            }
        }
    }
}

enum IterEnum<'a> {
    Map(std::collections::hash_map::Iter<'a, BV, BV>),
    Bytes(&'a [u8]),
}

pub struct IntoIter(Box<dyn Iterator<Item = (BV, BV)>>);

impl Iterator for IntoIter {
    type Item = (BV, BV);

    fn next(&mut self) -> Option<Self::Item> {
        self.0.next()
    }
}

impl IntoIterator for Headers {
    type Item = (BV, BV);
    type IntoIter = IntoIter;

    fn into_iter(self) -> Self::IntoIter {
        match self.0 {
            InnerHeaders::Map(m) => IntoIter(Box::new(m.into_iter())),
            InnerHeaders::Bytes(b) => IntoIter(Box::new(IntoIterBytes(b))),
        }
    }
}

struct IntoIterBytes(BV);

impl Iterator for IntoIterBytes {
    type Item = (BV, BV);

    fn next(&mut self) -> Option<Self::Item> {
        if self.0.len() < 4 {
            return None;
        }
        let (kl, vl) = (get2(&self.0) as usize, get2(&self.0) as usize);
        let kvl = kl + vl;
        self.0 = self.0.split_off(4);
        if kvl > self.0.len() {
            self.0.clear();
            return None;
        }
        let mut value = self.0.split_off(kl);
        let rest = value.split_off(vl);
        let key = mem::replace(&mut self.0, rest);
        let kv = (key, value);
        Some(kv)
    }
}

impl From<HM> for Headers {
    fn from(m: HM) -> Self {
        Self(InnerHeaders::Map(m))
    }
}

impl From<Headers> for HM {
    fn from(h: Headers) -> HM {
        match h.try_into_map() {
            Ok(m) => m,
            Err(h) => h.into_iter().collect(),
        }
    }
}

impl TryFrom<BV> for Headers {
    type Error = BV;

    fn try_from(b: BV) -> Result<Self, Self::Error> {
        Ok(Self(InnerHeaders::Bytes(b)))
    }
}

impl From<Headers> for BV {
    fn from(h: Headers) -> BV {
        let m = match h.0 {
            InnerHeaders::Bytes(b) => return b,
            InnerHeaders::Map(m) => m,
        };
        let mut b = Vec::new();
        for (mut k, mut v) in m {
            append2(&mut b, k.len() as u16);
            append2(&mut b, v.len() as u16);
            b.append(&mut k);
            b.append(&mut v);
        }
        b
    }
}
