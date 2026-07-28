package cache

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
)

type Prefetcher interface {
	// این تابع هر بار که یک درخواست خواندن به کش می‌آید صدا زده می‌شود
	// تا الگوریتم بتواند الگوها را شناسایی کند
	Inspect(req *memprotocol.ReadReq)
	// این تابع آدرس‌هایی که باید پیش‌واکشی شوند را برمی‌گرداند
	// الگوریتم گام‌دار این تابع را پیاده‌سازی خواهد کرد
	GetPrefetchAddresses() []uint64
	// ریست کردن وضعیت پیش‌واکشی (مثلا وقتی Miss رخ می‌دهد)
	Reset()
}

// DummyPrefetcher یک پیاده‌سازی توخالی برای تست زیرساخت شماست
type DummyPrefetcher struct{}

func (p *DummyPrefetcher) Inspect(req *memprotocol.ReadReq) {}
func (p *DummyPrefetcher) GetPrefetchAddresses() []uint64   { return nil }
func (p *DummyPrefetcher) Reset()                           {}
