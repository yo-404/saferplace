export type TranslationFile = {
    common: Partial<{
        email: string
        backend: string
        description: string
        submittedAtTime: string
        reportStatus: string
    }>
    action: Partial<{
        useEmail: string
        useBackend: string
        viewIncidents: string
        viewIncident: string
        submitReport: string
        retakePhoto: string
        takePhoto: string
    }>,
    phrases: Partial<{
        addToHomeScreen: string
        reportSuccessfullySubmitted: string
        beforeYouReport: string
        contactAuthoritiesFirst: string
        usingReportLocation: string
        incidentDescriptionPlaceholder: string
    }>,
    /** resolution are not partial as we need the description for every one */
    resolution: {
        inReview: string
        accepted: string
        alerted: string
        rejected: string
    }
}
