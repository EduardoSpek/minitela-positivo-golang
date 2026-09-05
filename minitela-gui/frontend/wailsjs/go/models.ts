export namespace main {
	
	export class scheduleRule {
	    enabled: boolean;
	    time: string;
	    page: number;
	    brightness: number;
	
	    static createFrom(source: any = {}) {
	        return new scheduleRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.time = source["time"];
	        this.page = source["page"];
	        this.brightness = source["brightness"];
	    }
	}

}

