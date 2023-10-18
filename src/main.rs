use std::collections::HashMap;
use std::fs;
//use std::io::{prelude::*, BufReader};
use std::iter::Peekable;
use std::rc::Rc;
use std::str::from_utf8 as str_from_utf8;

macro_rules! die {
    ($code:expr, $($args:tt)*) => {{
        ::std::eprintln!($($args)*);
        ::std::process::exit($code)
    }}
}

const VERSION_MAJOR: &str = "0";
const VERSION_MINOR: &str = "1";

fn main() {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    let args_strs = args.iter().map(|s| s.as_str()).collect::<Vec<_>>();
    match args_strs.as_slice() {
        ["version"] => print_version(),
        ["mod", "init", path] => create_mod_file(path),
        ["build", rest @ ..] => build_files(rest),
        _ => die!(0, "Usage: jtrpc <ARGS>"),
    }
}

fn print_version() {
    println!("Version: {VERSION_MAJOR}.{VERSION_MINOR}");
}

fn create_mod_file(path: &str) {
    let contents = format!("module {path}\nversion {VERSION_MAJOR}.{VERSION_MINOR}",);
    if let Err(e) = fs::write("jtrpc.mod", contents) {
        die!(2, "Error writing jtrpc.mod: {e}");
    }
}

fn build_files(infiles: &[&str]) {
    for infile in infiles {
        let content = match fs::read_to_string(infile) {
            Ok(s) => s,
            Err(e) => {
                eprintln!("Error opening file: {e}");
                continue;
            }
        };
        let file = match JtFile::parse_str(&content) {
            Ok(f) => f,
            Err(e) => {
                eprintln!("{e}");
                continue;
            }
        };
        println!("{file:#?}");
    }
}

type Res<T> = Result<T, Box<dyn std::error::Error>>;
type Line<'a> = (usize, &'a str);

#[allow(dead_code)]
#[derive(Clone, Debug)]
struct JtFile {
    name: Rc<str>,
    // Name, Struct
    structs: HashMap<Rc<str>, Struct>,
    funcs: HashMap<Rc<str>, Func>,
}

impl JtFile {
    fn parse_str(s: &str) -> Res<Self> {
        let mut file = JtFile::default();

        let mut lines = s
            .lines()
            .enumerate()
            .map(|(i, s)| (i + 1, s.trim()))
            .peekable();
        // Parse the file
        while let Some((i, line)) = lines.peek() {
            let i = *i;
            let Some(first) = line.split_ascii_whitespace().next() else {
                lines.next();
                continue;
            };
            match first {
                "import" => {
                    lines.next();
                }
                "option" => {
                    lines.next();
                }
                "struct" => {
                    let res = Struct::parse_lines(&mut lines)?;
                    if file.structs.contains_key(&res.name) {
                        return Err(format!("Line {i}: duplicate struct \"{}\"", res.name).into());
                    }
                    file.structs.insert(res.name.clone(), res);
                }
                "fn" => {
                    let func = Func::parse_line(lines.next().unwrap())?;
                    if file.funcs.contains_key(&func.name) {
                        return Err(format!("Line {i}: duplicate struct \"{}\"", func.name).into());
                    }
                    file.funcs.insert(func.name.clone(), func);
                }
                _ => {
                    lines.next();
                }
            }
        }
        Ok(file)
    }
}

impl Default for JtFile {
    fn default() -> Self {
        Self {
            name: "".into(),
            structs: HashMap::new(),
            funcs: HashMap::new(),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct Func {
    name: Rc<str>,
    // The name of the struct and whether it's a stream
    input: (Rc<str>, bool),
    output: (Rc<str>, bool),
}

impl Func {
    fn parse_line<'a>(line: Line<'a>) -> Res<Self> {
        let (i, line) = line;
        // Check for the "fn" keyword
        let rest = match line.split_once(|c: char| c.is_ascii_whitespace()) {
            Some(("fn", rest)) => rest.trim(),
            Some(_) => {
                return Err(format!(r#"Line {i}: expected keyword "fn", got "{line}""#).into())
            }
            None => return Err(format!("Line {i}: expected function definition").into()),
        };

        // Get the name
        let Some(end) = rest.find('(') else {
            return Err(format!("Line {i}: expected \"(\"").into());
        };
        let name = &rest[..end];
        if !is_valid_name(name) {
            return Err(format!("Line {i}: invalid name \"{name}\"").into());
        }

        // Get the input
        let start = end;
        let rest = &rest[start + 1..];
        let Some(end) = rest.find("):") else {
            return Err(format!("Line {i}: expected \"):\"").into());
        };
        let mut input = rest[..end].trim();
        let stream_input = match input.as_bytes() {
            [b'[', rest @ .., b']'] => {
                input = str_from_utf8(rest).unwrap();
                true
            }
            _ => false,
        };
        if !is_valid_name(input) {
            return Err(format!("Line {i}: invalid name \"{input}\"").into());
        }

        // Get the output
        if !rest.ends_with(';') {
            return Err(format!("Line {i}: missing ending semicolon").into());
        }
        let mut output = rest[end + 2..rest.len() - 1].trim();
        let stream_output = match output.as_bytes() {
            [b'[', rest @ .., b']'] => {
                output = str_from_utf8(rest).unwrap();
                true
            }
            _ => false,
        };
        if !is_valid_name(output) {
            return Err(format!("Line {i}: invalid name \"{output}\"").into());
        }
        Ok(Self {
            name: name.into(),
            input: (input.into(), stream_input),
            output: (output.into(), stream_output),
        })
    }
}

impl Default for Func {
    fn default() -> Self {
        Self {
            name: "".into(),
            input: ("".into(), false),
            output: ("".into(), false),
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
struct StructOptions {}

#[derive(Clone, Debug, PartialEq, Eq)]
struct Struct {
    name: Rc<str>,
    options: Vec<StructOptions>,
    fields: Vec<Rc<Field>>,
    reordered_fields: Vec<Rc<Field>>,
}

impl Struct {
    fn new(name: impl AsRef<str>) -> Self {
        Self {
            name: name.as_ref().into(),
            ..Self::default()
        }
    }

    fn parse_lines<'a>(lines: &mut Peekable<impl Iterator<Item = Line<'a>>>) -> Res<Self> {
        let Some((i, first)) = lines.next() else {
            return Err("Missing struct definition line".into());
        };

        // Parse the name
        let mut parts = first.split_ascii_whitespace().collect::<Vec<_>>();
        if parts.len() < 2 {
            return Err(format!(
                r#"Line {i}: invalid struct definition, expected "struct Name {{""#
            )
            .into());
        }
        if parts[0] != "struct" {
            return Err(format!("Line {i}: expected keyword \"struct\", got {}", parts[0]).into());
        }
        if parts[1].ends_with('{') {
            parts[1] = &parts[1][..parts[1].len() - 1];
            parts.push("{");
        }
        if parts.len() != 3 {
            return Err(format!(
                r#"Line {i}: invalid struct definition, expected "struct Name {{""#
            )
            .into());
        }
        if !is_valid_name(parts[1]) {
            return Err(format!("Line {i}: invalid name \"{}\"", parts[1]).into());
        }
        if parts[2] != "{" {
            return Err(format!("Line {i}: missing \"{{\" at end of line").into());
        }

        // Parse the rest
        let mut res = Self::new(parts[1].to_string());
        while let Some((i, line)) = lines.next() {
            if line == "}" {
                return Ok(res);
            }

            let Some((name, type_)) = line.split_once(':') else {
                return Err(format!("Line {i}: expected \":\" in field definition").into());
            };
            // Parse the name
            let name = name.trim();
            if !is_valid_name(name) {
                return Err(format!("Line {i}: \"{name}\" is not a valid field name").into());
            }
            // Parse the type
            let type_ = type_.trim();
            if !type_.ends_with(';') {
                return Err(format!("Line {i}: missing ending semicolon").into());
            }
            let type_: Type = type_[..type_.len() - 1]
                .parse()
                .map_err(|e| format!("Line {i}: {e}"))?;
            res.fields.push(Rc::new(Field {
                name: name.into(),
                type_,
            }));
        }
        Err(format!("Missing closing \"}}\" for struct starting on line {i}").into())
    }

    fn reorder_fields(&mut self) {
        let mut sizes = self
            .fields
            .iter()
            .map(|f| f.type_.size_bits().unwrap_or(0))
            .enumerate()
            .collect::<Vec<_>>();
        self.reordered_fields.clone_from_slice(&self.fields);
        sizes.sort_by(|(_, size1), (_, size2)| size1.cmp(size2));
        let mut target_bits = 64;
        let mut other_target = 32;
        loop {
            //!
        }
    }
}

impl Default for Struct {
    fn default() -> Self {
        Self {
            name: "".into(),
            options: Vec::new(),
            fields: Vec::new(),
            reordered_fields: Vec::new(),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct Field {
    name: Rc<str>,
    type_: Type,
}

#[allow(dead_code)]
#[derive(Clone, PartialEq, Eq, Debug)]
enum Type {
    I(usize),
    U(usize),
    Uv,
    Iv,
    F32,
    F64,
    Bool,
    // Size, null-terminated
    Str(Option<usize>, bool),
    // Type, Size
    Array(Box<Type>, Option<usize>),
    // Key, Value, Size
    Map(Box<Type>, Box<Type>, Option<usize>),
    Struct(Rc<str>),
    Optional(Box<Type>),
}

#[allow(dead_code)]
impl Type {
    fn is_composite(&self) -> bool {
        matches!(
            self,
            Type::Array(_, _) | Type::Map(_, _, _) | Type::Struct(_),
        )
    }

    // Returns the exact size of the type (in bits) if the exact size can be calculated, and none
    // otherwise.
    fn size_bits(&self) -> Option<usize> {
        use Type::*;
        match self {
            I(n) | U(n) => Some(*n),
            Uv | Iv => None,
            F32 => Some(32),
            F64 => Some(64),
            Bool => Some(1),
            Str(Some(n), _) => Some(n * 8),
            Str(None, _) => None,
            Array(t, Some(n)) => Some(n * t.size_bits()?),
            Array(_, None) => None,
            Map(k, v, Some(n)) => Some(n * k.size_bits()? + n * v.size_bits()?),
            Map(_, _, None) => None,
            // TODO: Struct size
            Struct(_) => None,
            Optional(t) => Some(1 + t.size_bits()?),
        }
    }

    // Returns the minimum size of the type in bits.
    fn min_size_bits(&self) -> usize {
        use Type::*;
        match self {
            I(n) | U(n) => *n,
            Uv | Iv => 8,
            F32 => 32,
            F64 => 64,
            Bool => 1,
            Str(Some(_), true) => 8,
            Str(Some(n), false) => n * 8,
            Str(None, true) => 8,
            // Length of encoded len
            Str(None, false) => 32,
            Array(t, Some(n)) => n * t.min_size_bits(),
            // Length of encoded len
            Array(_, None) => 32,
            Map(k, v, Some(n)) => n * k.min_size_bits() + n * v.min_size_bits(),
            //
            Map(_, _, None) => 32,
            Struct(_) => todo!(),
            Optional(_) => 1,
        }
    }
}

impl std::str::FromStr for Type {
    type Err = Box<dyn std::error::Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        use Type::*;
        let bytes = s.as_bytes();
        if bytes.len() == 0 {
            Err(format!("missing type"))?;
        }
        match bytes {
            [b'i', rest @ ..] if rest.iter().all(u8::is_ascii_digit) => Ok(I(s[1..].parse()?)),
            [b'u', rest @ ..] if rest.iter().all(u8::is_ascii_digit) => Ok(U(s[1..].parse()?)),
            b"uv" => Ok(Uv),
            b"iv" => Ok(Iv),
            b"f32" => Ok(F32),
            b"f64" => Ok(F64),
            b"bool" => Ok(Bool),
            b"str" => Ok(Str(None, false)),
            [b'[', inner @ .., b']'] => {
                let s = str_from_utf8(inner).unwrap();
                let (rest, len) = s
                    .split_once(';')
                    .map(|(rest, len)| len.trim().parse().map(|l| (rest, Some(l))))
                    .transpose()?
                    .unwrap_or((s, None));
                if let Some((key, value)) = rest.split_once(',') {
                    Ok(Map(
                        Box::new(key.trim().parse()?),
                        Box::new(value.trim().parse()?),
                        len,
                    ))
                } else {
                    Ok(Array(Box::new(rest.trim().parse()?), len))
                }
            }
            _ => {
                if s.split('.').any(|p| !is_valid_name(p)) {
                    Err(format!("invalid type: \"{}\"", s).into())
                } else {
                    Ok(Struct(s.into()))
                }
            }
        }
    }
}

fn is_valid_name(s: &str) -> bool {
    // TODO: Can a variable name start with an underscore?
    s.as_bytes()
        .get(0)
        .map(u8::is_ascii_alphabetic)
        .unwrap_or(false)
        && s.bytes().all(|s| s.is_ascii_alphanumeric() || s == b'_')
}
