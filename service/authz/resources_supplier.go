package authz

const ResourceSupplier = "supplier"

var (
	SupplierRead   = Permission{Resource: ResourceSupplier, Action: ActionRead}
	SupplierWrite  = Permission{Resource: ResourceSupplier, Action: ActionWrite}
	SupplierSettle = Permission{Resource: ResourceSupplier, Action: "settle"}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceSupplier,
		LabelKey: "Supplier management",
		Actions: []ActionDefinition{
			{Action: ActionRead, LabelKey: "Read suppliers", DescriptionKey: "View suppliers, channels, monitoring and settlement records.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: ActionWrite, LabelKey: "Edit suppliers", DescriptionKey: "Create suppliers, manage channel associations and edit draft bills.", DefaultRoles: []string{BuiltInRoleAdmin}},
			{Action: "settle", LabelKey: "Settle supplier bills", DescriptionKey: "Confirm supplier bills and create settlement allocations.", DefaultRoles: []string{BuiltInRoleRoot}},
		},
	})
}
