// Pages showing a single resource register a friendly label for their UUID
// segment (e.g. the workspace name) so breadcrumbs stay readable.
class BreadcrumbLabels {
	labels = $state<Record<string, string>>({});

	set(segment: string, label: string) {
		this.labels[segment] = label;
	}
}

export const crumbs = new BreadcrumbLabels();
