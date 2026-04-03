const path = require("path");
const WebSocket = require("ws");
const protobuf = require("protobufjs");

// WebSocket 服务地址
const WS_URL = process.env.WS_URL || "ws://127.0.0.1:9000";

// proto 文件路径
const PROTO_PATH = path.join(__dirname, "../internal/pb/barrage.proto");

// 从命令行读取参数
const mode = process.argv[2] || "listen";
const roomId = Number(process.argv[3] || 1);
const userId = Number(process.argv[4] || Date.now());
const content = process.argv[5] || "hello barrage";

// 主流程入口
async function main() {
    const root = await protobuf.load(PROTO_PATH);

    const Frame = root.lookupType("barrage.Frame");
    const JoinRoomReq = root.lookupType("barrage.JoinRoomReq");
    const ChatMsg = root.lookupType("barrage.ChatMsg");
    const Heartbeat = root.lookupType("barrage.Heartbeat");
    const OpType = root.lookupEnum("barrage.OpType").values;

    const ws = new WebSocket(WS_URL);

    ws.on("open", () => {
        console.log(`[客户端 ${userId}] WebSocket 已连接: ${WS_URL}`);

        // 连接成功后先自动加入房间
        sendJoinRoom(ws, Frame, JoinRoomReq, OpType, roomId, userId);

        // 根据模式决定后续行为
        if (mode === "send") {
            setTimeout(() => {
                sendChat(ws, Frame, ChatMsg, OpType, roomId, userId, content);
            }, 500);
        }

        if (mode === "heartbeat") {
            setInterval(() => {
                sendHeartbeat(ws, Frame, Heartbeat, OpType, roomId, userId);
            }, 5000);
        }
    });

    ws.on("message", (data, isBinary) => {
        try {
            const buffer = Buffer.isBuffer(data) ? data : Buffer.from(data);

            if (!isBinary) {
                console.log(`[客户端 ${userId}] 收到非二进制消息:`, buffer.toString());
                return;
            }

            const frame = Frame.decode(buffer);

            console.log(`[客户端 ${userId}] 收到广播 Frame:`, {
                op: frame.op,
                roomId: Number(frame.room_id || frame.roomId || 0),
                userId: Number(frame.user_id || frame.userId || 0),
                timestamp: Number(frame.timestamp || 0),
                payloadLength: frame.payload ? frame.payload.length : 0
            });

            // 如果是广播消息，则继续解析为 ChatMsg
            if (frame.op === OpType.OP_BROADCAST && frame.payload) {
                const chat = ChatMsg.decode(frame.payload);

                console.log(`[客户端 ${userId}] 收到聊天广播内容:`, {
                    content: chat.content
                });
            }
        } catch (err) {
            console.error(`[客户端 ${userId}] 解析消息失败:`, err);
        }
    });

    ws.on("close", (code, reason) => {
        console.log(`[客户端 ${userId}] 连接关闭: code=${code}, reason=${reason.toString()}`);
    });

    ws.on("error", (err) => {
        console.error(`[客户端 ${userId}] 连接异常:`, err);
    });
}

// 发送加入房间请求
function sendJoinRoom(ws, Frame, JoinRoomReq, OpType, roomId, userId) {
  const joinPayload = JoinRoomReq.encode(
    JoinRoomReq.create({
      roomId: roomId,
      userId: userId,
      isPremium: false
    })
  ).finish();

  const frameBuffer = Frame.encode(
    Frame.create({
      op: OpType.OP_JOIN_ROOM,
      roomId: roomId,
      userId: userId,
      payload: joinPayload,
      timestamp: Date.now()
    })
    ).finish();

    ws.send(frameBuffer);
    console.log(`[客户端 ${userId}] 已发送 JoinRoom: roomId=${roomId}`);
}

// 发送聊天消息
function sendChat(ws, Frame, ChatMsg, OpType, roomId, userId, content) {
  const chatPayload = ChatMsg.encode(
    ChatMsg.create({
      content
        })
    ).finish();

  const frameBuffer = Frame.encode(
    Frame.create({
      op: OpType.OP_CHAT,
      roomId: roomId,
      userId: userId,
      payload: chatPayload,
      timestamp: Date.now()
    })
    ).finish();

    ws.send(frameBuffer);
    console.log(`[客户端 ${userId}] 已发送 Chat: roomId=${roomId}, content=${content}`);
}

// 发送心跳消息
function sendHeartbeat(ws, Frame, Heartbeat, OpType, roomId, userId) {
  const heartbeatPayload = Heartbeat.encode(
    Heartbeat.create({
      clientTs: Date.now()
    })
  ).finish();

  const frameBuffer = Frame.encode(
    Frame.create({
      op: OpType.OP_HEARTBEAT,
      roomId: roomId,
      userId: userId,
      payload: heartbeatPayload,
      timestamp: Date.now()
    })
    ).finish();

    ws.send(frameBuffer);
    console.log(`[客户端 ${userId}] 已发送 Heartbeat`);
}

main().catch((err) => {
    console.error("测试客户端启动失败:", err);
    process.exit(1);
});
