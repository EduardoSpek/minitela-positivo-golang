export namespace main {
	
	export class scheduleRule {
	    enabled: boolean;
	    start: string;
	    end: string;
	    page: number;
	    brightness: number;
	
	    static createFrom(source: any = {}) {
	        return new scheduleRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.start = source["start"];
	        this.end = source["end"];
	        this.page = source["page"];
	        this.brightness = source["brightness"];
	    }
	}

}

