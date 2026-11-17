// bot 是一个压测客户端。它模拟真实玩家的完整行为：
// 连接 → 登录 → 进场景 → 持续移动 → 定时心跳。
//
// 用法：
//
//	go run ./tools/bot -n 500 -addr 127.0.0.1:7001
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	pnet "openworld/internal/net"
	"openworld/internal/protocol"
	"openworld/pkg/authtoken"
)

// 全局统计，所有 bot 共享。
var (
	statSceneSwitch atomic.Int64
	statOnline      atomic.Int64
	statFrames      atomic.Int64
	statAttrs       atomic.Int64
	statRRTSum      atomic.Int64
	statRTTNum      atomic.Int64
	statErr         atomic.Int64
	statLogined     atomic.Int64
	statHits        atomic.Int64
	statKills       atomic.Int64
)

// Bot 是一个虚拟玩家。
type Bot struct {
	id int

	conn     net.Conn
	entityID uint64

	x, y, z float32

	loginCh chan *protocol.LoginResp
	enterCh chan *protocol.EnterSceneResp
	kickCh  chan string

	hbSeq       atomic.Uint64
	hbSentNanos atomic.Int64

	frames atomic.Int64
	// dir 是 -walk x 模式下沿 X 轴的行进方向（+1 东 / -1 西）。
	dir float32
	// walkLo/walkHi 是本机自己的折返区间，等于全局 -min-x/-max-x 再
	// 扩到包含自己的出生点。
	//
	// 为什么要按机器人生成而不是全局共用：出生点是世界按玩家名散列算的，
	// 落在 [0, worldWidth) 而不是落在 [-min-x, -max-x) 里。如果不管出生点
	// 直接套全局区间，落在区间外的机器人会在第一个移动包就把坐标拽到区间
	// 端点，服务端按超速钳制并计入 moveRejected——看起来像"边界处漏包"，
	// 实际是压测工具自己制造的一次瞬移。实测 120 个机器人里有 7 个中招，
	// 制造了 159 次虚假的服务端拒绝计数。
	walkLo, walkHi float32
	// entered 标记是否已经进过场景，用来识别"第二条 EnterSceneResp"——
	// 那就是一次跨场景迁移的客户端视角。
	entered atomic.Bool

	// 攻击行为：记录视野内最近看到的实体 ID，周期性攻击它。
	// 用 atomic 存，因为 target 由 readLoop 写入、主循环读取。
	lastTarget atomic.Uint64
}

// walkCfg 是 -walk 相关参数的全局快照。
//
// 放进包里而不是逐层传参：它会出现在每个机器人最内层的移动 tick 里，
// 为它加一个参数只是把同一个变量抄到四个函数签名上。
var walkCfg struct {
	mode       string
	minX, maxX float32
}

// attackEvery 是攻击指令的发送间隔。攻击频率比移动低得多（普攻 1 秒
// 一发就够），但用 500ms 让"技能 CD 未转好"的拒绝路径也能被压到。
const attackEvery = 500 * time.Millisecond

// attackEnabled 是否开启攻击行为，由 -attack 决定。包级变量而不是传参，
// 理由和 walkCfg 一样：它出现在最内层的攻击 tick 里。
var attackEnabled bool

func newBot(id int) *Bot {
	return &Bot{
		id:      id,
		loginCh: make(chan *protocol.LoginResp, 1),
		enterCh: make(chan *protocol.EnterSceneResp, 1),
		kickCh:  make(chan string, 1),
	}
}

func (b *Bot) run(ctx context.Context, addr, secret string, moveEvery time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	b.conn = conn
	defer func() {
		_ = conn.Close()
		if b.entityID != 0 {
			statOnline.Add(-1)
		}
	}()

	go b.readLoop()

	// 1. 登录
	token := authtoken.Sign(secret, fmt.Sprintf("bot-%d", b.id), time.Now().Unix())
	if err := b.send(protocol.MsgC2SLogin, (&protocol.LoginReq{Token: token}).Encode()); err != nil {
		return err
	}

	select {
	case resp := <-b.loginCh:
		if resp.Code != protocol.CodeOK {
			return fmt.Errorf("login rejected: code=%d reason=%s", resp.Code, resp.Reason)
		}
	case <-time.After(5 * time.Second):
		return errors.New("login timeout")
	case <-ctx.Done():
		return nil
	}

	// 2. 进场景
	if err := b.send(protocol.MsgC2SEnterScene, (&protocol.EnterSceneReq{}).Encode()); err != nil {
		return err
	}

	select {
	case resp := <-b.enterCh:
		if resp.Code != protocol.CodeOK {
			return fmt.Errorf("enter scene rejected: code=%d reason=%s", resp.Code, resp.Reason)
		}
		b.entityID = resp.EntityID
		b.x, b.y, b.z = resp.X, resp.Y, resp.Z
		// 折返区间扩到包含出生点：区间外的出生点会让第一个包变成一次瞬移，
		// 被服务端的超速校验钳制掉，凭空刷出一批 moveRejected。
		b.walkLo, b.walkHi = walkCfg.minX, walkCfg.maxX
		if b.x < b.walkLo {
			b.walkLo = b.x
		}
		if b.x > b.walkHi {
			b.walkHi = b.x
		}
		statOnline.Add(1)
		statLogined.Add(1)
	case <-time.After(5 * time.Second):
		return errors.New("enter scene timeout")
	case <-ctx.Done():
		return nil
	}

	// 3. 主循环：移动 + 心跳 + 攻击
	moveTicker := time.NewTicker(moveEvery)
	hbTicker := time.NewTicker(5 * time.Second)
	atkTicker := time.NewTicker(attackEvery)
	defer moveTicker.Stop()
	defer hbTicker.Stop()
	defer atkTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case msg := <-b.kickCh:
			return fmt.Errorf("kicked: %s", msg)

		case <-hbTicker.C:
			seq := b.hbSeq.Add(1)
			b.hbSentNanos.Store(time.Now().UnixNano())
			_ = b.send(protocol.MsgC2SHeartbeat, (&protocol.HeartbeatReq{
				Seq: seq, ClientTime: time.Now().UnixMilli(),
			}).Encode())

		case <-atkTicker.C:
			// 攻击：对最近看到的目标施放技能。技能 ID 轮换，压测 CD 与多技能。
			if attackEnabled && b.entityID != 0 {
				if tgt := b.lastTarget.Load(); tgt != 0 {
					skill := uint8(1 + b.id%3) // 1=普攻 2=重击 3=旋风斩
					_ = b.send(protocol.MsgC2SAttack, (&protocol.AttackReq{
						TargetID: tgt, SkillID: skill,
					}).Encode())
				}
			}

		case <-moveTicker.C:
			if b.entityID == 0 {
				continue
			}
			dt := moveEvery.Seconds()
			// 速度取上限的 80%，保证不会被服务端的超速钳制误伤。
			stepLen := float32(6.0 * dt * 0.8)
			if walkCfg.mode == "x" {
				// 沿 X 轴来回走，专门用来跨越场景边界。
				// 折返点不取反方向向量，而是直接夹住再翻转——
				// 取反会让位置跳变，服务端的超速校验会把它当异常位移。
				b.x += b.dir * stepLen
				if b.x > b.walkHi {
					b.x = b.walkHi
					b.dir = -1
				} else if b.x < b.walkLo {
					b.x = b.walkLo
					b.dir = 1
				}
			} else {
				ang := rand.Float64() * 2 * math.Pi
				b.x += float32(math.Cos(ang)) * stepLen
				b.z += float32(math.Sin(ang)) * stepLen
				// 钳制而不是取反：取反会造成一次近百米的瞬移，
				// 服务端的超速校验会把它当成异常位移钳制掉（表现为 moveRejected 上涨）。
				//
				// 这段只在随机游走时生效。-walk x 模式必须能走出这个框，
				// 否则机器人永远到不了场景边界，跨界路径根本测不到——
				// 第一次跑跨场景压测时就被这个残留的 ±495 挡住了，
				// 表现为"0 次迁移"，看起来像服务端的问题。
				if b.x > 495 {
					b.x = 495
				}
				if b.x < -495 {
					b.x = -495
				}
				if b.z > 495 {
					b.z = 495
				}
				if b.z < -495 {
					b.z = -495
				}
			}
			if b.id == 0 && os.Getenv("BOT_DEBUG") != "" {
				log.Printf("[bot0] x=%.1f z=%.1f dir=%.0f", b.x, b.z, b.dir)
			}
			_ = b.send(protocol.MsgC2SMove, (&protocol.MoveReq{
				X: b.x, Y: b.y, Z: b.z, ClientTime: time.Now().UnixMilli(),
			}).Encode())
		}
	}
}

func (b *Bot) send(msgID uint16, body []byte) error {
	_ = b.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := b.conn.Write(pnet.PackFrame(msgID, body))
	return err
}

func (b *Bot) readLoop() {
	hdr := make([]byte, protocol.HeaderSize)
	for {
		_ = b.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		if _, err := io.ReadFull(b.conn, hdr); err != nil {
			return
		}
		msgID, bodyLen, ok := pnet.UnpackHeader(hdr)
		if !ok {
			return
		}
		var body []byte
		if bodyLen > 0 {
			body = make([]byte, bodyLen)
			if _, err := io.ReadFull(b.conn, body); err != nil {
				return
			}
		}
		b.handle(msgID, body)
	}
}

func (b *Bot) handle(msgID uint16, body []byte) {
	switch msgID {
	case protocol.MsgS2CLoginResp:
		var m protocol.LoginResp
		if m.Decode(body) == nil {
			select {
			case b.loginCh <- &m:
			default:
			}
		}
	case protocol.MsgS2CEnterScene:
		var m protocol.EnterSceneResp
		if m.Decode(body) == nil {
			if b.entered.Swap(true) {
				// 第二次收到 EnterSceneResp = 跨场景迁移完成。
				statSceneSwitch.Add(1)
			}
			select {
			case b.enterCh <- &m:
			default:
			}
		}
	case protocol.MsgS2CKick:
		var m protocol.Kick
		if m.Decode(body) == nil {
			select {
			case b.kickCh <- m.Reason:
			default:
			}
		}
	case protocol.MsgS2CHeartbeat:
		var m protocol.HeartbeatResp
		if m.Decode(body) == nil {
			if sent := b.hbSentNanos.Load(); sent > 0 {
				rtt := time.Now().UnixNano() - sent
				statRRTSum.Add(rtt)
				statRTTNum.Add(1)
			}
		}
	case protocol.MsgS2CEntityState, protocol.MsgS2CEntityEnter, protocol.MsgS2CEntityLeave:
		b.frames.Add(1)
		statFrames.Add(1)
		// 记录一个可攻击目标：EntityState/Enter 里的实体都可以打。
		// 机器人不区分敌我，直接打最近看到的那一个，服务端会校验敌对关系。
		if msgID == protocol.MsgS2CEntityState {
			var m protocol.EntityState
			if m.Decode(body) == nil && m.EntityID != b.entityID {
				b.lastTarget.Store(m.EntityID)
			}
		}
	case protocol.MsgS2CEntityAttr:
		// 属性包单独计数：它验证的是"ECS 组件 -> System -> 网络"这条链路。
		statAttrs.Add(1)
	case protocol.MsgS2CCombatHit:
		statHits.Add(1)
		var m protocol.CombatHit
		if m.Decode(body) == nil && m.Killed {
			statKills.Add(1)
		}
	}
}

func main() {
	var (
		addr     = flag.String("addr", "127.0.0.1:7001", "网关地址")
		n        = flag.Int("n", 100, "并发机器人数量")
		secret   = flag.String("secret", "dev-secret-change-me", "令牌密钥，必须与服务端一致")
		move     = flag.Duration("move", 100*time.Millisecond, "移动指令发送间隔")
		duration = flag.Duration("duration", 0, "运行时长，0 表示一直跑到 Ctrl+C")
		rampup   = flag.Duration("rampup", 5*time.Millisecond, "每个机器人之间的连接间隔")
		walk     = flag.String("walk", "random", "行走模式：random 随机游走 / x 沿 X 轴往返（用于压测跨场景迁移）")
		minX     = flag.Float64("min-x", -495, "-walk x 的西侧折返点")
		maxX     = flag.Float64("max-x", 495, "-walk x 的东侧折返点")
		attack   = flag.Bool("attack", false, "是否攻击视野内目标（压测战斗管线）")
	)
	flag.Parse()

	if *n <= 0 {
		log.Fatal("n must be > 0")
	}

	walkCfg.mode = *walk
	walkCfg.minX = float32(*minX)
	walkCfg.maxX = float32(*maxX)
	attackEnabled = *attack

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, *duration)
		defer c()
	}

	log.Printf("starting %d bots against %s (move=%s, rampup=%s)", *n, *addr, *move, *rampup)

	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			b := newBot(id)
			b.dir = 1
			if err := b.run(ctx, *addr, *secret, *move); err != nil {
				statErr.Add(1)
				if id < 3 || statErr.Load()%100 == 0 {
					log.Printf("bot %d failed: %v", id, err)
				}
			}
		}(i)
		time.Sleep(*rampup)
	}

	// 每秒打印一次汇总。
	reportDone := make(chan struct{})
	go func() {
		defer close(reportDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		start := time.Now()
		var lastFrames, lastOnline, lastAttrs int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				frames := statFrames.Load()
				online := statOnline.Load()
				var avgRTT time.Duration
				if num := statRTTNum.Load(); num > 0 {
					avgRTT = time.Duration(statRRTSum.Load() / num)
				}
				log.Printf("[%5.0fs] online=%d logined=%d failed=%d fps=%d/s avgRTT=%s attr/s=%d switches=%d hits=%d kills=%d",
					time.Since(start).Seconds(), online, statLogined.Load(), statErr.Load(),
					frames-lastFrames, avgRTT.Round(time.Microsecond), statAttrs.Load()-lastAttrs,
					statSceneSwitch.Load(), statHits.Load(), statKills.Load())
				lastAttrs = statAttrs.Load()
				lastOnline = online
				_ = lastOnline
				lastFrames = frames
			}
		}
	}()

	wg.Wait()
	cancel()
	<-reportDone

	log.Printf("done. logined=%d failed=%d online=%d totalFrames=%d totalAttrs=%d sceneSwitches=%d hits=%d kills=%d",
		statLogined.Load(), statErr.Load(), statOnline.Load(), statFrames.Load(), statAttrs.Load(),
		statSceneSwitch.Load(), statHits.Load(), statKills.Load())
}
