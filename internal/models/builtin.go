package models

// Builtin returns pi's provider with this id, when the bundled catalog has
// one. The app's provider picker lists them all; models.json only overlays a
// base URL or an API key on top.
func Builtin(id string) (BuiltinProvider, bool) {
	for _, p := range BuiltinProviders {
		if p.ID == id {
			return p, true
		}
	}
	return BuiltinProvider{}, false
}

// BuiltinModels returns the models pi ships for a provider, empty when it
// ships none or the id is not one of its own.
func BuiltinModels(id string) []BuiltinModel {
	p, ok := Builtin(id)
	if !ok {
		return nil
	}
	return p.Models
}
