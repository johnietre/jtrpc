module JtRPC

import Sockets, Base.Threads

export
    Client, Request, Response, Stream, Message,
    dial, send!, recv!, close!, isclosed,
    HeaderBytes, HeaderDict, Headers,
    get_header, set_header!, parse_headers!,
    MAX_HEADERS_LEN,
    FLAG_STREAM_MSG,
    REQ_FLAG_STREAM, REQ_FLAG_TIMEOUT, REQ_FLAG_CANCEL,
    MSG_FLAG_NOT_STREAM, MSG_FLAG_TOO_LARGE, MSG_FLAG_CLOSE,
    STATUS_OK,
    STATUS_NOT_FOUND, STATUS_IS_STREAM, STATUS_NOT_STREAM,
    STATUS_BAD_REQUEST, STATUS_UNAUTHORIZED, STATUS_BODY_TOO_LARGE,
    STATUS_INVALID_INITIAL_BYTES, STATUS_BAD_VERSION,
    STATUS_INTERNAL_SERVER_ERROR,
    Byte, Bytes

const Byte = UInt8
const Bytes = Vector{Byte}
const MAX_HEADERS_LEN = 1 << 16 - 1

const FLAG_STREAM_MSG = Byte(0b1000_0000)

const REQ_FLAG_STREAM = Byte(0b0100_0000)
const REQ_FLAG_TIMEOUT = Byte(0b0000_0010)
const REQ_FLAG_CANCEL = Byte(0b0000_0001)

const MSG_FLAG_NOT_STREAM = Byte(0b0100_0000)
const MSG_FLAG_TOO_LARGE = Byte(0b0010_0000)
const MSG_FLAG_CLOSE = Byte(0b0000_0001)

#= Non-Errors =#
const STATUS_OK = Byte(0)

#= Client Errors =#
const STATUS_NOT_FOUND = Byte(128)
const STATUS_IS_STREAM = Byte(129)
const STATUS_NOT_STREAM = Byte(130)
const STATUS_BAD_REQUEST = Byte(131)
const STATUS_UNAUTHORIZED = Byte(132)
const STATUS_BODY_TOO_LARGE = Byte(133)
const STATUS_INVALID_INITIAL_BYTES = Byte(160)
const STATUS_BAD_VERSION = Byte(161)

#= Server Errors =#
const STATUS_INTERNAL_SERVER_ERROR = Byte(192)

function has_stream_flag(flags::Byte)::Bool
    flags & REQ_FLAG_STREAM != 0
end

const HeaderBytes = Bytes
const HeaderDict = Dict{String, String}
const Headers = Union{HeaderBytes, HeaderDict}

mutable struct Request
    id::UInt64
    flags::UInt8
    path::String
    headers::HeaderDict
    body::Bytes
end

function Request(path::String; stream=false)::Request
    flags = stream ? REQ_FLAG_STREAM : 0
    Request(0, flags, path, HeaderDict(), Bytes())
end

Request(path::String, body::Bytes) = Request(0, 0, path, HeaderDict(), body)
Request(path::String, body::String) = Request(path, Bytes(body))
Request(
    path::String, headers::HeaderDict, body::Bytes,
) = Request(0, 0, path, headers, body)
Request(
    path::String, headers::HeaderDict, body::String,
) = Request(0, 0, path, headers, Bytes(body))

function get_header(req::Request, key::String)::Union{String, nothing}
    get(req.headers, key, nothing)
end

function set_header!(req::Request, key::String, value::String)
    req.headers[key] = value
end

mutable struct Message
    flags::UInt8
    body::Bytes
end

Message(close=false) = Message(close ? MSG_FLAG_CLOSE : 0, [])
Message(body::Bytes) = Message(0, body)
Message(body::String) = Message(0, Bytes(body))

struct StreamClosedError <: Exception
    message::Union{Message, Nothing}
    client_closed::Bool
end

StreamClosedError() = StreamClosedError(nothing, true)
StreamClosedError(msg::Union{Message, Nothing}) = StreamClosedError(msg, false)
StreamClosedError(client_closed::Bool) = StreamClosedError(nothing, client_closed)

abstract type AbstractClient end

mutable struct Stream
    client::AbstractClient
    req::Request
    chan::Channel{Message}
    @atomic isclosed::Bool
    @atomic close_err::Union{StreamClosedError, Nothing}
end

function recv!(stream::Stream; throw_error::Bool=false)::Union{Message, Nothing}
    if isclosed(stream)
        !throw_error && return nothing
        err = @atomic stream.close_err
        throw(err === nothing ? StreamClosedError() : err)
    end
    try
        take!(stream.chan)
    catch
        throw_error && rethrow()
    end
end

function send!(stream::Stream, msg::Message; throw_error::Bool=false)::Bool
    if isclosed(stream)
        !throw_error && return false
        err = @atomic stream.close_err
        throw(err === nothing ? StreamClosedError() : err)
    end
    try
        send_to_stream!(stream, msg)
        true
    catch
        throw_error && rethrow()
        false
    end
end

function send_to_stream!(stream::Stream, msg::Message)
    msg.flags |= FLAG_STREAM_MSG
    lock(stream.client.conn_write_lock) do
        writeall(
            stream.client.conn,
            [to_le_bytes(stream.req.id); msg.flags;
            to_le_bytes(UInt64(length(msg.body)))],
        )
        writeall(stream.client.conn, msg.body)
    end
end

function close!(stream::Stream)
    close_stream!(stream, true)
end

function close!(stream::Stream, msg::Message)
    close_stream!(stream, true, msg)
end

function close_stream!(
    stream::Stream, send_close::Bool, msg::Union{Message, Nothing}=nothing;
    close_msg::Union{Message, Nothing}=nothing,
)
    (@atomicswap stream.isclosed = true) && return
    if close_msg === nothing
        @atomic stream.close_err = StreamClosedError(true)
    else
        @atomic stream.close_err = StreamClosedError(close_msg)
    end
    client = stream.client
    lock(() -> delete!(client.streams, stream.req.id), client.streams_lock)
    close(stream.chan)
    !send_close && return
    try
        if msg === nothing
            msg = Message(MSG_FLAG_CLOSE, [])
        else
            msg.flags |= MSG_FLAG_CLOSE
        end
        send_to_stream!(stream, msg)
    catch
    end
end

function isclosed(stream::Stream)::Bool
    @atomic stream.isclosed
end

mutable struct Response
    req_id::UInt64
    flags::Byte
    status_code::Byte
    path::String
    headers::Headers
    body::Bytes
    stream::Union{Stream, Nothing}
end

function read_response(r::IO)::Response
    buf = read(r, 20)
    req_id = from_le_bytes8(buf)
    flags = buf[9]
    status_code = buf[10]
    hl = from_le_bytes2(buf[11:12])
    bl = from_le_bytes8(buf[13:end])
    headers = hl == 0 ? Bytes() : read(r, hl)
    body = bl == 0 ? Bytes() : read(r, hl)
    Response(req_id, flags, status_code, "", headers, body, nothing)
end

function parse_headers!(resp::Response)::HeaderDict
    typeof(resp.headers) == HeaderDict && return resp.headers
    resp.headers = parse_headers(resp.headers)
    resp.headers
end

function parse_headers(hb::HeaderBytes)::HeaderDict
    headers, left = HeaderDict(), length(hb)
    # TODO: Do something different on invalid headers?
    while left != 0
        left < 4 && return headers
        kl = from_le_bytes2(hb[1:2])
        vl = from_le_bytes2(hb[3:4])
        kvl = kl + vl
    hb = hb[5:end]
    left -= 4

    left < kvl && return headers
key, hb = hb[1:kl], hb[kl+1:end]
val, hb = hb[1:vl], hb[vl+1:end]
headers[String(key)] = String(val)
left -= kvl
  end
  headers
end

const RespChan = Channel{Response}

const ReqTup = Tuple{Request, RespChan}

mutable struct Client <: AbstractClient
    addr::Tuple{Sockets.IPAddr, UInt16}
    conn::Sockets.TCPSocket
    conn_write_lock::ReentrantLock

    @atomic isclosed::Bool
    # This is error is set when the client has failed in reading from/listening
    # to the server. The client is closed when this occurs.
    @atomic listen_err::Union{Exception, Nothing}

    req_counter::Threads.Atomic{UInt64}
    reqs::Dict{UInt64, ReqTup}
    reqs_lock::ReentrantLock
    streams::Dict{UInt64, Stream}
    streams_lock::ReentrantLock
end

const INITIAL_BYTES = Vector{Byte}([
    0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x05,
    'j', 't', 'r', 'p', 'c',
])
const INITIAL_BYTES_VER = Vector{Byte}([INITIAL_BYTES; 0x00; 0x01])

struct ConnectException{T} <: Exception
    msg::String
    data::T
end

ConnectException(msg::String) = ConnectException(msg, nothing)
ConnectException(data::T) where {T} = ConnectException("", data)

# Throws ConnectException with Response received if the error was in
# establishing the connection (received error response).
function dial(host::Sockets.IPAddr, port::UInt16)::Client
    conn = Sockets.connect(host, port)

    write(conn, INITIAL_BYTES_VER)
    resp = read_response(conn)
    if resp.status_code != STATUS_OK
        throw(ConnectException(resp))
    end
    
    client = Client(
        (host, port), conn, ReentrantLock(),
        false, nothing,
        Threads.Atomic{UInt64}(0),
        Dict{UInt64, Request}(), ReentrantLock(),
        Dict{UInt64, Stream}(), ReentrantLock(),
    )
    Threads.@spawn listen_conn!(client)
    client
end

function dial(host::Sockets.IPAddr, port::Integer)::Client
    dial(host, UInt16(port))
end

function listen_conn!(client::Client)
    # TODO: Pool
    buf = Bytes(undef, 9)
    try
        while true
            readbytes!(client.conn, buf, 9)
            id = from_le_bytes8(buf)
            flags = buf[9]

            if flags & FLAG_STREAM_MSG != 0
                stream = lock(client.streams_lock) do
                    get(client.streams, id, nothing)
                end
                if stream !== nothing
                    handle_stream_msg!(client, stream, buf)
                end
                continue
            end

            req_tup = lock(() -> pop!(client.reqs, id, nothing), client.reqs_lock)
            if req_tup !== nothing
                try
                    handle_resp!(client, req_tup, buf)
                catch
                    # TODO: With exception?
                    # TODO: Do here (vs in handle_resp!)?
                    close(req_tup[2])
                    rethrow()
                end
            end
        end
    catch e
        @atomic client.listen_err = e
    end
    #catch; rethrow(); end
    #catch; end
    close!(client)
end

function handle_resp!(client::Client, req_tup::ReqTup, buf::Bytes)
    (req, chan) = req_tup
    flags = buf[9]

    # Read the status and lengths
    # Get the status code and whether there's a stream
    readbytes!(client.conn, buf, 11)
    status_code = buf[1]
    has_stream = status_code == STATUS_OK && req.flags & REQ_FLAG_STREAM != 0

    # Get the headers and body length
    hl = from_le_bytes2(buf[2:3])
    # TODO: Check for appropriate body length (i.e., lack of body)?
    bl = from_le_bytes8(buf[4:11])

    hb = read(client.conn, hl)
    bb = read(client.conn, bl)
    stream = if has_stream
        # TODO: Channel length
        stream = Stream(client, req, Channel{Message}(Inf), false, nothing)
        lock(() -> client.streams[req.id] = stream, client.streams_lock)
        stream
    else
        nothing
    end
    try
        put!(
            chan,
            Response(req.id, flags, status_code, req.path, hb, bb, stream),
        )
        # TODO: With exception?
        close(chan)
    catch
        rethrow()
    end
end

function handle_stream_msg!(client::Client, stream::Stream, buf::Bytes)
    flags = buf[9]
    readbytes!(client.conn, buf, 8)
    ml = from_le_bytes8(buf)
    mb = read(client.conn, ml)
    if flags & MSG_FLAG_CLOSE != 0
        close_stream!(stream, false; close_msg=Message(flags, mb))
        return
    end
    try
        put!(stream.chan, Message(flags, mb))
    catch
        # Stream (channel) already closed by client
        #close_stream!(stream, true)
        close_stream!(stream, false)
    end
    return
    # TODO: Send msg anyway?
    close_stream!(stream, false)
end

const CLIENT_CLOSED = ErrorException("client closed")
const HEADER_TOO_LONG = ErrorException("header too long")

function send!(client::Client, req::Request)::RespChan
    isclosed(client) && throw(CLIENT_CLOSED)
    while true
        # TODO: Check duplicate ID
        req.id = Threads.atomic_add!(client.req_counter, UInt64(1))
        break
    end

    # Start with ID bytes and flags
    req_bytes = push!(to_le_bytes(req.id), req.flags)
    # Append request path length
    append!(req_bytes, to_le_bytes(UInt16(length(req.path))))

    # Marshal the headers and get the length
    headers_bytes = Bytes()
    for kv in req.headers
        key, val = kv[1], kv[2]
        length(key) > MAX_HEADERS_LEN && throw(HEADER_TOO_LONG)
        length(val) > MAX_HEADERS_LEN && throw(HEADER_TOO_LONG)
        append!(
            headers_bytes,
            to_le_bytes(UInt16(length(key))), to_le_bytes(UInt16(length(val))),
            key, val,
        )
        length(headers_bytes) > MAX_HEADERS_LEN && throw(HEADER_TOO_LONG)
    end
    # Append headers and body length
    bl = UInt64(length(req.body))
    append!(
        req_bytes,
        to_le_bytes(UInt16(length(headers_bytes))),
        to_le_bytes(bl),
    )
    # Add the path and headers
    append!(req_bytes, req.path, headers_bytes)
    # Write the bytes (and body) to the request
    chan = Channel{Response}(1)
    lock(client.conn_write_lock) do
        lock(client.reqs_lock) do
            writeall(client.conn, req_bytes)
            if length(req.body) != 0
                writeall(client.conn, req.body)
            end
            client.reqs[req.id] = (req, chan)
        end
    end
    chan
end

function close!(client::Client)
    (@atomicswap client.isclosed = true) && return
    lock(() -> close(client.conn), client.conn_write_lock)
    lock(client.streams_lock) do
        for (id, stream) in client.streams
            close!(stream)
            delete!(client.streams, id)
        end
    end
    lock(client.reqs_lock) do
        for (id, (_, ch)) in client.reqs
            close(ch)
            delete!(client.reqs, id)
        end
    end
end

function isclosed(client::Client)::Bool
    @atomic client.isclosed
end

function recv!(chan::Channel{T})::Union{T, Nothing} where {T}
    try; take!(chan); catch; end
end

function to_le_bytes(u::UInt16)::Bytes
    [UInt8(u), UInt8(u >> 8)]
end

function from_le_bytes2(b::Bytes)::UInt16
    UInt16(b[1]) | UInt16(b[2]) << 8
end

function to_le_bytes(u::UInt64)::Bytes
    [UInt8(u), UInt8(u >> 8), UInt8(u >> 16), UInt8(u >> 24),
    UInt8(u >> 32), UInt8(u >> 40), UInt8(u >> 48), UInt8(u >> 56)]
end

function from_le_bytes8(b::Bytes)::UInt64
    UInt64(b[1]) | UInt64(b[2]) << 8 |
    UInt64(b[3]) << 16 | UInt64(b[4]) << 24 |
    UInt64(b[5]) << 32 | UInt64(b[6]) << 40 |
    UInt64(b[7]) << 48 | UInt64(b[8]) << 54
end

function writeall(w::IO, bytes::Bytes)
    left = length(bytes)
    while left != 0
        nw = write(w, bytes)
        bytes = bytes[nw+1:end]
        left -= nw
    end
    flush(w)
end

end # module JtRPC
