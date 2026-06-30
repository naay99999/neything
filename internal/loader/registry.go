package loader

type Registry struct {
	loaders []Loader
}

func NewRegistry(loaders ...Loader) Registry {
	return Registry{loaders: loaders}
}

func (r Registry) Dispatch(path string) (Loader, bool) {
	for _, l := range r.loaders {
		if l.Supports(path) {
			return l, true
		}
	}
	return nil, false
}
