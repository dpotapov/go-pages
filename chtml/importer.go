package chtml

// Importer acts as a factory for components. It is invoked when a <c:NAME> element is encountered.
type Importer interface {
	Import(name string) (Component, error)
}

type builtinImporter struct {
	cattr CAttr
}

func (i *builtinImporter) Import(name string) (Component, error) {
	switch name {
	case "attr":
		return &i.cattr, nil
	default:
		return nil, ErrComponentNotFound
	}
}
