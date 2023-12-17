- All Byte encodings are little endian

Initial Connect
=====================
On initial connection, the following bytes must be sent. All subsequent writes/sends/whatever do not need these bytes.
- Intial bytes: "\x00\x00\x00\x00\x00\x01\x05jtrpc<MAJOR VERSION BYTE><MINOR VERSION BYTE>"
Server responds with an OK response.

Request
=====================
<8-byte REQUEST ID>
<1-byte BIT FLAGS (top bit not set)>
<2-byte PATH LEN>
<2-byte HEADERS LEN>
<8-byte BODY LEN>
<PATH>
<HEADERS>
<BODY>

Response
=====================
<8-byte REQUEST ID>
<1-byte BIT FLAGS (top bit not set)>
<1-byte STATUS CODE>
<2-byte HEADERS LEN>
<8-byte BODY LEN>
<HEADERS>
<BODY>

Stream Message
=====================
<8-byte REQUEST ID>
<1-byte BIT FLAGS (top bit set)>
<8-byte MESSAGE LEN>
<BODY>

Status Codes
=====================
0 - 127 == non-error, 128 - 255 == error
Non-Errors
- 0 == OK
- 1 == Partial error (not a total failure, but something wrong still happened)
Client Errors
- 128 == Not found
- 129 == Is stream (the path requested is a stream but stream not requested)
- 130 == Not stream (the path requested isn't a stream but stream requested)
- 131 == Bad request
- 132 == Unauthorized
- 133 == Body Too Large
- 160 == Invalid Initial Bytes (only sent as response to initial connection setup)
- 161 == Bad Version (the version isn't supported by the server)
- 191 == Timed out
Server Errors
- 192 == Internal server error

Bit Flags (bits 0 (bottom) - 7 (top))
=====================
- Universal
  - 7th (top) bit == stream message
- Request
  - 7th (top) bit not set
  - 6th bit == stream
    - Requesting a stream
    - Shouldn't have body (body len should be 0)
  - 1st bit == timeout
    - The request may be timed out (expects time length hint as first header)
  - 0th bit == cancel
    - Cancel the request (doesn't expect a body length, should be omitted)
    - Client should send on timeout as server may not be exactly in sync
    - Server won't (shouldn't) send response so client can delete request
- Stream Message
  - 7th bit (top) set
  - 6th bit == Not stream
    - No stream with passed request
  - 5th bit == too large
    - A message sent was too large
  - 2nd bit == Pong
  - 1st bit == Ping
    - The ping/pong flags are useful for making sure the connection is established. Clients are required to make sure the stream is ready to receive on before the server is ready to send messages so it may be useful for servers to send this (and clients to wait for this) before continuing on
  - 0th bit == close

# TODO
- [ ] What to do on timeouts? Close connection?
- [ ] What's sent for various errors (like too long status/flag)?
