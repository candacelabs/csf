package blobfish

import "strconv"

// This file is the whole of the seam between the store and the card. Nothing in
// store.go, replica.go or view.go names a wire field, a region or an event, and
// nothing in the generated widget knows there are three zones behind it.

// ReportFields is one store view as the replicaReport event carries it.
func ReportFields(view StoreView) map[string]string {
	return map[string]string{
		BlobfishEventReplicaReportFieldGeneration:   strconv.FormatUint(view.Generation, 10),
		BlobfishEventReplicaReportFieldStorageClass: view.StorageClass,
		BlobfishEventReplicaReportFieldObjects:      strconv.Itoa(view.Objects),
		BlobfishEventReplicaReportFieldWriteAcks:    strconv.Itoa(view.WriteAcks),
		BlobfishEventReplicaReportFieldLaggingZones: strconv.Itoa(view.LaggingZones),
		BlobfishEventReplicaReportFieldWritable:     strconv.FormatBool(view.Writable),
		BlobfishEventReplicaReportFieldReadable:     strconv.FormatBool(view.Readable),
	}
}
