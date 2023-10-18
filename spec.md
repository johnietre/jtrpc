# Spec

# Types
## Scalars
- i*
	- Integer with a given arbitrary number of bits
- u*
	- Unsigned integer with a given arbitrary number of bits
	- Allow specifying of byte order?
- uv
	- Varint (unsigned)
- iv
	- Signed varint (two's complement)
	- TODO: Change name?
	- TODO: Tag to accept encoding type (or create new type) (two's complement vs zigzag)
- f32
	- 32-bit float
- f64
	- 64-bit float
- bool
	- Boolean value (may be encoded in 1 bit if possible)
- str
	- A length-encoded (or null-terminated?) string
	- TODO: Null terminated string type or tag for null-terminated without length
	- TODO: Tag for specified length bounds

## Composite
### Array
- Syntax: [TYPE] or [TYPE;LENGTH]
	- [bool]/[bool;length] is translated into [u1]/[u1;LENGTH]

### Map
- Syntax: [KEY_TYPE,VALUE_TYPE] or [KEY_TYPE,VALUE_TYPE;LENGTH]
	- Key [i32,i32], [str,[str,iv;5]], [u12 , bool; 10]
- Only scalars can be keys
	- TODO: Can all scalars be keys (e.g., u256 and varints)
	
### Struct
TODO
	
## Other (qualifiers)
### Optional
- Syntax: TYPE?
	- Ex) i23?, string?, [uv?]?
- Representation:
	- A single byte/bit specifying whether it is present

# Encoding

## Struct
Fields are serialized in definition order unless a field reordering is allowed and it makes more sense for a field to be reordered (see [Field Reordering](#Field-Reordering)).7

### Field Reordering
Unless specified otherwise, the fields of a struct or map may be reordered for compactness. For example, if we have the following definition:
```
struct Struct {
	field1: u41;
	field2: f64;
	field3: i23;
}
```
it will (TODO: Is this guaranteed?) be reordered as follows:
```
struct Struct {
	field1: u41;
	field2: i23;
	field3: f64;
}
```
This way, it can be encoded in 128-bits more easily and without values having to cross word (64-bit) boundaries.

# JtRPC

## Request
- Structure:
```
<INITIAL BYTES - 16 bytes>
<8-byte REQUEST ID>
<2-byte FN NAME/PATH LEN>
<FN NAME/PATH>
<LENGTHS - 12 bytes>
<HEADERS - >= 0 bytes>
<BODY - >= 0 bytes>
```

### Initial bytes
"\x00\x00\x00\x00\x00\x01\x05jtrpc<MAJOR VERSION><MINOR VERSION>"   
<MAJOR VERSION> and <MINOR VERSION> are the major and minor version in 2-byte little endian

### Request ID
- The request ID, with little endian encoding

### Fn name/path
- 2-byte name/path length (little endian)
- The name/path
    - The function can be specified as a path by separating modules (TODO: What are the called?) using dot (.) separaters

### Lengths
- 4-byte little endian headers length
- 8-byte little endian body length

### Headers
- Required headers (come first)?

#### Header Key-Value Pairs
- 2-byte key length and 2-byte value length

### Body

### Trailers

## Response
- Structure:
```
<8-byte REQUEST ID>
<1-byte STATUS>
<2-byte ERROR LEN>
<ERROR>
<LENGTHS - 16 bytes>
<HEADERS - >= 0 bytes>
<BODY - >= 0 bytes>
<TRAILERS - >= 0 bytes>
```

### Request ID
- Same as ID request from Request

### Status
- Status code

### Error
- Only present if status is not OK; if status is OK, encoding/decoding this is skipped (including the length of the error message, which would be zero)
- 2-byte (little endian) error message length
- Error message
- The headers, body, and trailers may still be present even if there is an error so the lengths should still be checked

### Lengths
- 4-byte little endian headers length
- 8-byte little endian body length
- 4-byte little endian trailers length

### Headers
- Request ID header (same format as in request header)

### Body

### Trailers
- Same format as headers

## HTTP

### Request
- Headers: JTRPC-REQ-ID, JTRPC-FUNC
	
# Todo
- 16-bit float?
- Decimal type?
