package Cx

import data.generic.common as common_lib
import data.generic.k8s as k8sLib

types := {"initContainers", "containers"}

# container defines runAsUser
checkUser(specInfo, container, containerType, document, metadata) = result {
	uid := container.securityContext.runAsUser
	to_number(uid) < 10000

	result := {
		"documentId": document.id,
		"searchKey": sprintf("metadata.name={{%s}}.%s.%s.name={{%s}}.securityContext.runAsUser=%d", [metadata.name, specInfo.path, containerType, container.name, uid]),
		"issueType": "IncorrectValue",
		"keyExpectedValue": sprintf("metadata.name={{%s}}.%s.%s.name={{%s}}.securityContext.runAsUser should be set to a UID >= 10000", [metadata.name, specInfo.path, containerType, container.name]),
		"keyActualValue": sprintf("metadata.name={{%s}}.%s.%s.name={{%s}}.securityContext.runAsUser is set to a low UID", [metadata.name, specInfo.path, containerType, container.name]),
	}
}

# pod defines runAsUser and container inherits this setting
checkUser(specInfo, container, containerType, document, metadata) = result {
	containerCtx := object.get(container, "securityContext", {})
	not common_lib.valid_key(containerCtx, "runAsUser")

	uid := specInfo.spec.securityContext.runAsUser
	to_number(uid) < 10000

	result := {
		"documentId": document.id,
		"searchKey": sprintf("metadata.name={{%s}}.%s.securityContext.runAsUser=%d", [metadata.name, specInfo.path, uid]),
		"issueType": "IncorrectValue",
		"keyExpectedValue": sprintf("metadata.name={{%s}}.%s.securityContext.runAsUser should be set to a UID >= 10000", [metadata.name, specInfo.path]),
		"keyActualValue": sprintf("metadata.name={{%s}}.%s.securityContext.runAsUser is set to a low UID", [metadata.name, specInfo.path]),
	}
}

# neither pod nor container define runAsUser
checkUser(specInfo, container, containerType, document, metadata) = result {
	specCtx := object.get(specInfo.spec, "securityContext", {})
	not common_lib.valid_key(specCtx, "runAsUser")

	containerCtx := object.get(container, "securityContext", {})
	not common_lib.valid_key(containerCtx, "runAsUser")

	result := {
		"documentId": document.id,
		"searchKey": sprintf("metadata.name={{%s}}.%s.%s.name={{%s}}.securityContext", [metadata.name, specInfo.path, containerType, container.name]),
		"issueType": "MissingAttribute",
		"keyExpectedValue": sprintf("metadata.name={{%s}}.%s.%s.name={{%s}}.securityContext.runAsUser should be defined", [metadata.name, specInfo.path, containerType, container.name]),
		"keyActualValue": sprintf("metadata.name={{%s}}.%s.%s.name={{%s}}.securityContext.runAsUser is undefined", [metadata.name, specInfo.path, containerType, container.name]),
	}
}

CxPolicy[result] {
	document := input.document[i]
	metadata := document.metadata

	specInfo := k8sLib.getSpecInfo(document)

	result := checkUser(specInfo, specInfo.spec[types[x]][_], types[x], document, metadata)
}
