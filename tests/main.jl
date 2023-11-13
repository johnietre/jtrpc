#push!(LOAD_PATH, "../julia")
include("../julia/JtRPC/src/JtRPC.jl")

#import JtRPC
using Sockets

function die(msg::String)
    println(msg)
    exit()
end

#client = JtRPC.dial(ip"127.0.0.1", 8008)
client = JtRPC.dial(ip"127.0.0.1", 8080)

req = JtRPC.Request("/echo", "no and yes")
req.headers["h1"] = "value1"
req.headers["h2"] = "value2"
req.headers["header3579"] = "value3579"
resp_chan = JtRPC.send!(client, req)
resp = JtRPC.recv!(resp_chan)
resp === nothing && die("expected response")
JtRPC.parse_headers!(resp)
println("Response: ", resp)
println("Body: ", String(resp.body))
println()

req = JtRPC.Request("/yes", "no and yes")
resp_chan = JtRPC.send!(client, req)
resp = JtRPC.recv!(resp_chan)
resp === nothing && die("expected response")
JtRPC.parse_headers!(resp)
println("Response: ", resp)
println("Body: ", String(resp.body))
println()

req = JtRPC.Request("/yes")
resp_chan = JtRPC.send!(client, req)
resp = JtRPC.recv!(resp_chan)
resp === nothing && die("expected response")
JtRPC.parse_headers!(resp)
println("Response: ", resp)
println("Body: ", String(resp.body))
println()

req = JtRPC.Request("/no", "no and yes")
resp_chan = JtRPC.send!(client, req)
resp = JtRPC.recv!(resp_chan)
resp === nothing && die("expected response")
JtRPC.parse_headers!(resp)
println("Response: ", resp)
println("Body: ", String(resp.body))
println()

req = JtRPC.Request("/stream"; stream=true)
req.headers["stream_header1"] = "value1"
req.headers["stream_header2"] = "value2"
req.headers["stream_header3579"] = "value3579"
resp_chan = JtRPC.send!(client, req)
resp = JtRPC.recv!(resp_chan)
resp === nothing && die("expected response")
JtRPC.parse_headers!(resp)
println("Response: ", resp)
println("Body: ", String(resp.body))
stream = resp.stream
stream === nothing && error("Expected stream")
while true
    global stream
    print("Message: ")
    msg_str = readline()
    msg_str == "exit" && break
    !JtRPC.send!(stream, JtRPC.Message(msg_str)) && error("Didn't send message")
    msg = JtRPC.recv!(stream)
    msg === nothing && error("Expected message")
    println("Received: ", String(msg.body))
end
JtRPC.close!(stream)
