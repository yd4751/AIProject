package persist

// LayeredStore 把"在线缓存"和"最终档案"叠成一层，实现"内存→Redis→MySQL"
// 蓝图：Redis 扛高频读、MySQL 扛持久真相。业务层（player 包）只看到 Store
// 接口，完全不知道底下有几层、谁先谁后。
//
// 语义：
//   - Get：先查缓存（Redis）。命中直接返回；未命中再查最终层（MySQL），
//     查到后把结果回填缓存（下次同玩家读就走缓存），查不到返回 (nil, false)。
//   - Put：先写最终层、再写缓存。最终层是权威真相，必须先落；缓存是派生，
//     后写即使失败也只是少一次命中，不会造成"缓存有、真相没有"的脏读。
//   - Delete：两层都删（缓存删失败不影响真相，所以忽略缓存错误）。
type LayeredStore struct {
	cache Store // 在线缓存（如 Redis）
	final Store // 最终档案（如 MySQL）
}

// NewLayeredStore 构造分层存储。cache 和 final 都不能为 nil。
func NewLayeredStore(cache, final Store) *LayeredStore {
	return &LayeredStore{cache: cache, final: final}
}

// Get 先查缓存，未命中查最终层并回填缓存。
func (s *LayeredStore) Get(player string) ([]byte, bool) {
	if b, ok := s.cache.Get(player); ok {
		return b, true
	}
	b, ok := s.final.Get(player)
	if !ok {
		return nil, false
	}
	// 回填缓存：忽略错误（缓存不可用不致命）。
	_ = s.cache.Put(player, b)
	return b, true
}

// Put 先写最终层再写缓存。
func (s *LayeredStore) Put(player string, data []byte) error {
	if err := s.final.Put(player, data); err != nil {
		return err
	}
	return s.cache.Put(player, data)
}

// Delete 两层都删，缓存错误忽略。
func (s *LayeredStore) Delete(player string) error {
	_ = s.cache.Delete(player)
	return s.final.Delete(player)
}

// Close 释放两层后端的资源（连接池等）。任一层若未实现 Closer 则跳过。
func (s *LayeredStore) Close() error {
	var firstErr error
	if c, ok := s.cache.(Closer); ok {
		if err := c.Close(); err != nil {
			firstErr = err
		}
	}
	if f, ok := s.final.(Closer); ok {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
