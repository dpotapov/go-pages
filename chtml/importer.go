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

// importerWithAttr decorates a user importer to always support builtin attribute components.
type importerWithAttr struct {
	base    Importer
	builtin builtinImporter
}

func (w *importerWithAttr) Import(name string) (Component, error) {
	if name == "attr" {
		return w.builtin.Import(name)
	}
	if w.base == nil {
		return nil, ErrImportNotAllowed
	}
	return w.base.Import(name)
}
