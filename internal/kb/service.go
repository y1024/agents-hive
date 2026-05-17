package kb

import "github.com/chef-guo/agents-hive/internal/agentquality"

type Service struct {
	store            Store
	resolver         BindingResolver
	summaryGenerator SummaryGenerator
	tokenCounter     TokenCounter
	assetUploader    AssetUploader
	qualityRecorder  QualityRecorder
	maxNodeIDs       int
	maxSectionBytes  int
}

type QualityRecorder interface {
	RecordKBQualityEvent(sessionID string, event agentquality.Event)
}

type ServiceOption func(*Service)

func NewService(store Store, opts ...ServiceOption) *Service {
	s := &Service{
		store:           store,
		resolver:        NewBindingResolver(store),
		tokenCounter:    EstimateTokenCounter{},
		maxNodeIDs:      8,
		maxSectionBytes: 64 * 1024,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithBindingResolver(resolver BindingResolver) ServiceOption {
	return func(s *Service) {
		s.resolver = resolver
	}
}

func WithSummaryGenerator(generator SummaryGenerator) ServiceOption {
	return func(s *Service) {
		s.summaryGenerator = generator
	}
}

func WithTokenCounter(counter TokenCounter) ServiceOption {
	return func(s *Service) {
		s.tokenCounter = counter
	}
}

func WithAssetUploader(uploader AssetUploader) ServiceOption {
	return func(s *Service) {
		s.assetUploader = uploader
	}
}

func WithQualityRecorder(recorder QualityRecorder) ServiceOption {
	return func(s *Service) {
		s.qualityRecorder = recorder
	}
}

func (s *Service) SetQualityRecorder(recorder QualityRecorder) {
	if s == nil {
		return
	}
	s.qualityRecorder = recorder
}

func WithSectionLimits(maxNodeIDs, maxBytes int) ServiceOption {
	return func(s *Service) {
		if maxNodeIDs > 0 {
			s.maxNodeIDs = maxNodeIDs
		}
		if maxBytes > 0 {
			s.maxSectionBytes = maxBytes
		}
	}
}
